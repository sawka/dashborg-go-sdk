package bufsrv

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// 106-byte header
// <version:2>|<dir:2><id:20>|<module:58>|<enc:12>|<body-len:11><newline:1>
// version is padded with "0" chars
// module, id are padded with spaces (trimmed-left when passed to other functions)
// bodylen is padded with "0" chars

// TODO SetDeadline
// TODO Push - hold push events for a while for reconnects.  deal with failed pushes.  send push feedback.

const DEADLINE = 30 * time.Second

const HEADERLEN = 106

const VERSIONOFFSET = 0
const VERSIONLEN = 2

const DIROFFSET = 2
const DIRLEN = 2

const IDOFFSET = 4
const IDLEN = 20

const MODULEOFFSET = 24
const MODULELEN = 58

const ENCOFFSET = 82
const ENCLEN = 12

const BODYOFFSET = 94
const BODYLENLEN = 11

const DIR_CALL = "CA"
const DIR_RTN = "RT"
const DIR_PUSH = "PU"
const DIR_PUSHREG = "RP"

const MAX_BODYSIZE = 1000000000
const HEADER_FMTSTRING = "%02d%2s%20s%58s%12s%011d\n"

var AllowedEncs map[string]bool
var RandSource rand.Source = rand.NewSource(time.Now().UnixNano())

func init() {
	AllowedEncs = make(map[string]bool)
	AllowedEncs["json"] = true
}

type PacketHeader struct {
	Version int
	Dir     string
	Id      string
	Module  string
	Enc     string
	BodyLen int
}

type FullPacket struct {
	Header PacketHeader
	Body   []byte
}

type Server struct {
	Lock       *sync.Mutex
	ListenConn net.Listener
	Handlers   map[string]Handler
	Running    bool
	Err        error
	Verbose    bool
	OpenConn   func(string, net.Conn)
	CloseConn  func(string, net.Conn, error)
	PushCtxMap map[string]*ServerSession
}

type ServerSession struct {
	S         *Server
	Lock      *sync.Mutex
	Conn      net.Conn
	ConnId    string
	IdNum     int64
	Err       error
	SendQueue chan FullPacket
	PushCtx   string
}

type PushFnType func(PushType) (interface{}, error)

type Client struct {
	Lock         *sync.Mutex
	Conn         net.Conn
	IdNum        int64
	Closed       bool
	Err          error
	OpenRequests map[string]chan ResponseType
	PushCtx      string
	PushFn       PushFnType
}

type ResponseType struct {
	Response  interface{}
	Error     string
	ConnError error
}

func (r ResponseType) Err() error {
	if r.ConnError != nil {
		return r.ConnError
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

type PushType struct {
	PushCtx     string
	PushRecvId  string // empty for KeepAlive
	PushPayload interface{}
}

type UnifiedResponseType struct {
	ResponseType
	PushType
}

type Handler interface {
	Handle(connId string, val interface{}) (interface{}, error)
}

type funcHandler struct {
	f func(connId string, val interface{}) (interface{}, error)
}

func (f funcHandler) Handle(connId string, val interface{}) (interface{}, error) {
	return f.f(connId, val)
}

func (ph PacketHeader) IsValid() bool {
	if ph.Version <= 0 || ph.Version > 100 {
		return false
	}
	if len(ph.Id) > IDLEN {
		return false
	}
	if ph.Dir != DIR_CALL && ph.Dir != DIR_RTN && ph.Dir != DIR_PUSH && ph.Dir != DIR_PUSHREG {
		return false
	}
	if len(ph.Module) > MODULELEN {
		return false
	}
	if len(ph.Enc) > ENCLEN {
		return false
	}
	if ph.BodyLen > MAX_BODYSIZE {
		return false
	}
	return true
}

func (ph PacketHeader) MakeHeader() string {
	rtn := fmt.Sprintf(HEADER_FMTSTRING, ph.Version, ph.Dir, ph.Id, ph.Module, ph.Enc, ph.BodyLen)
	return rtn
}

func ParseHeader(hs string) (PacketHeader, error) {
	ph := PacketHeader{}
	if len(hs) != HEADERLEN {
		return ph, fmt.Errorf("Header must be exactly %d bytes (was %d)", HEADERLEN, len(hs))
	}
	verStr := hs[VERSIONOFFSET : VERSIONOFFSET+VERSIONLEN]
	dirStr := hs[DIROFFSET : DIROFFSET+DIRLEN]
	idStr := hs[IDOFFSET : IDOFFSET+IDLEN]
	moduleStr := hs[MODULEOFFSET : MODULEOFFSET+MODULELEN]
	encStr := hs[ENCOFFSET : ENCOFFSET+ENCLEN]
	bodyLenStr := hs[BODYOFFSET : BODYOFFSET+BODYLENLEN]
	nlStr := hs[HEADERLEN-1 : HEADERLEN]
	if verStr != "01" {
		return ph, fmt.Errorf("Invalid Version (must be '01'), was '%s'", verStr)
	}
	if nlStr != "\n" {
		return ph, fmt.Errorf("Header must be terminated with a newline (was '%s')", nlStr)
	}
	bodyLen, err := strconv.Atoi(bodyLenStr)
	if err != nil {
		return ph, fmt.Errorf("could not parse bodylen (%s): %v", bodyLenStr, err)
	}
	if bodyLen < 0 || bodyLen > MAX_BODYSIZE {
		return ph, fmt.Errorf("bodylen is out of range (%d)", bodyLen)
	}
	ph.Version = 1
	ph.Dir = dirStr
	if dirStr != DIR_CALL && dirStr != DIR_RTN && dirStr != DIR_PUSH && dirStr != DIR_PUSHREG {
		return ph, fmt.Errorf("Invalid Dir, was '%s'", dirStr)
	}
	ph.Id = strings.TrimLeft(idStr, " ")
	ph.Module = strings.TrimLeft(moduleStr, " ")
	ph.Enc = strings.TrimLeft(encStr, " ")
	if !AllowedEncs[ph.Enc] {
		return ph, fmt.Errorf("Not a valid enc (was '%s')", ph.Enc)
	}
	ph.BodyLen = bodyLen
	if !ph.IsValid() {
		return ph, fmt.Errorf("Packet Header is not valid")
	}
	return ph, nil
}

func MakeServer() *Server {
	s := &Server{}
	s.Lock = &sync.Mutex{}
	s.Handlers = make(map[string]Handler)
	s.PushCtxMap = make(map[string]*ServerSession)
	return s
}

func (s *Server) AddHandlerFunc(module string, fn func(connId string, v interface{}) (interface{}, error)) {
	s.Handlers[module] = funcHandler{f: fn}
}

func (s *Server) AddHandler(module string, h Handler) {
	s.Handlers[module] = h
}

func (s *Server) DoPush(pushCtx string, pushRecvId string, payload interface{}) (interface{}, error) {
	s.Lock.Lock()
	ss := s.PushCtxMap[pushCtx]
	s.Lock.Unlock()
	if ss == nil {
		return nil, fmt.Errorf("No server session registered for pushctx[%s]", pushCtx)
	}
	pp := PushType{PushCtx: pushCtx, PushRecvId: pushRecvId, PushPayload: payload}
	barr, err := marshalJsonNoEnc(pp)
	if err != nil {
		return nil, fmt.Errorf("Error marshaling push payload: %v", err)
	}
	idnum := atomic.AddInt64(&ss.IdNum, 1)
	header := PacketHeader{Version: 1, Dir: DIR_PUSH, Id: strconv.FormatInt(idnum, 10), Module: "push", Enc: "json", BodyLen: len(barr)}
	if !header.IsValid() {
		return nil, fmt.Errorf("Invalid Packet Header")
	}
	ss.SendQueue <- FullPacket{Header: header, Body: barr}
	// TODO wait for push response
	return nil, nil
}

// returns EOFs
func readPacket(conn net.Conn, rtn interface{}) (*PacketHeader, error) {
	NoReadDeadline(conn)
	headerBuf := make([]byte, HEADERLEN)
	_, err := io.ReadFull(conn, headerBuf)
	if err != nil {
		return nil, err
	}
	ph, err := ParseHeader(string(headerBuf))
	if err != nil {
		return nil, err
	}
	SetReadDeadline(conn)
	bodyBuf := make([]byte, ph.BodyLen)
	_, err = io.ReadFull(conn, bodyBuf)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return nil, err
	}
	if ph.Enc != "json" {
		return nil, fmt.Errorf("Invalid enc (was '%s')", ph.Enc)
	}
	err = json.Unmarshal(bodyBuf, rtn)
	if err != nil {
		return nil, err
	}
	return &ph, nil
}

func (ss *ServerSession) writeRespPacket(conn net.Conn, h PacketHeader, resp ResponseType) error {
	barr, err := marshalJsonNoEnc(resp)
	if err != nil {
		barr, err = json.Marshal(ResponseType{Response: nil, Error: fmt.Sprintf("Error marshaling response: %v", err)})
		if err != nil {
			return err
		}
	}
	ph := PacketHeader{Version: h.Version, Dir: DIR_RTN, Id: h.Id, Module: h.Module, Enc: "json", BodyLen: len(barr)}
	if !ph.IsValid() {
		return errors.New("Invalid Packet Header")
	}
	ss.SendQueue <- FullPacket{Header: ph, Body: barr}
	return nil
}

func (s *Server) InfoLog(format string, a ...interface{}) {
	if s.Verbose {
		fmt.Printf(format, a...)
	}
}

func (s *Server) RegisterPushCtx(pushCtx string, ss *ServerSession) {
	if pushCtx == "" {
		return
	}
	s.Lock.Lock()
	defer s.Lock.Unlock()
	s.PushCtxMap[pushCtx] = ss
}

func (s *Server) UnregisterPushCtx(pushCtx string, ss *ServerSession) {
	if pushCtx == "" {
		return
	}
	s.Lock.Lock()
	defer s.Lock.Unlock()
	regSS := s.PushCtxMap[pushCtx]
	if regSS == ss {
		delete(s.PushCtxMap, pushCtx)
	}
}

func (ss *ServerSession) RunReadLoop() error {
	var connErr error
	for {
		var val interface{}
		ph, err := readPacket(ss.Conn, &val)
		if err != nil {
			if err != io.EOF {
				connErr = err
				ss.S.InfoLog("Error reading packet: %v\n", err)
			}
			break
		}
		var resp ResponseType
		if ph.Dir == DIR_RTN {
			// TODO push return
			continue
		} else if ph.Dir == DIR_PUSH {
			// invalid, cannot send push to server
			resp.Error = fmt.Sprintf("Cannot send PU (Push) packet to Server")
		} else if ph.Dir == DIR_PUSHREG {
			// no response sent for PUSHREG
			pushCtx, ok := val.(string)
			if !ok {
				fmt.Printf("invalid body type for PushReg pushCtx: %T", val)
				continue
			}
			ss.S.UnregisterPushCtx(ss.PushCtx, ss)
			ss.PushCtx = pushCtx
			ss.S.RegisterPushCtx(ss.PushCtx, ss)
			continue
		} else if ph.Dir == DIR_CALL {
			ss.S.InfoLog("got request %v\n", ph)
			handler := ss.S.Handlers[ph.Module]
			if handler == nil {
				resp.Error = fmt.Sprintf("No handler found for '%s'", ph.Module)
			} else {
				rv, err := handler.Handle(ss.ConnId, val)
				resp.Response = rv
				if err != nil {
					resp.Error = err.Error()
				}
			}
		} else {
			resp.Error = fmt.Sprintf("Invalid packet direction '%s'", ph.Dir)
		}
		err = ss.writeRespPacket(ss.Conn, *ph, resp)
		if err != nil {
			connErr = err
			ss.S.InfoLog("Error writing response: %v\n", err)
			break
		}
		ss.S.InfoLog("sent response %v\n", ph)
	}
	return connErr
}

func SetWriteDeadline(conn net.Conn) {
	now := time.Now()
	conn.SetWriteDeadline(now.Add(DEADLINE))
}

func SetReadDeadline(conn net.Conn) {
	now := time.Now()
	conn.SetReadDeadline(now.Add(DEADLINE))
}

func NoReadDeadline(conn net.Conn) {
	conn.SetReadDeadline(time.Time{})
}

func (ss *ServerSession) RunSendLoop() {
	for fullPacket := range ss.SendQueue {
		SetWriteDeadline(ss.Conn)
		_, err := ss.Conn.Write([]byte(fullPacket.Header.MakeHeader()))
		if err != nil {
			ss.Err = err
			ss.Conn.Close()
			fmt.Printf("Error in ss.RunSendLoop %v\n", ss.Err)
			return
		}
		SetWriteDeadline(ss.Conn)
		_, err = ss.Conn.Write(fullPacket.Body)
		if err != nil {
			ss.Err = err
			ss.Conn.Close()
			fmt.Printf("Error in ss.RunSendLoop %v\n", ss.Err)
			return
		}
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	s.InfoLog("Got connection %v\n", conn)
	connId := uuid.New().String()
	if s.OpenConn != nil {
		s.OpenConn(connId, conn)
	}
	var connErr error
	defer func() {
		if s.CloseConn != nil {
			s.CloseConn(connId, conn, connErr)
		}
	}()
	ss := &ServerSession{S: s, Lock: &sync.Mutex{}, Conn: conn, ConnId: connId}
	ss.SendQueue = make(chan FullPacket, 10)
	defer func() {
		s.UnregisterPushCtx(ss.PushCtx, ss)
		close(ss.SendQueue)
	}()
	go ss.RunSendLoop()
	connErr = ss.RunReadLoop()
	s.InfoLog("Closing connection %v\n", conn)
}

func (s *Server) ListenAndServe(addr string, tlsConfig *tls.Config) error {
	if s.ListenConn != nil {
		return errors.New("Server already started")
	}
	var ln net.Listener
	var err error
	if tlsConfig == nil {
		ln, err = net.Listen("tcp", addr)
	} else {
		ln, err = tls.Listen("tcp", addr, tlsConfig)
	}
	if err != nil {
		return err
	}
	s.ListenConn = ln
	log.Printf("BufSrv: Running server at %v | %T\n", addr, ln)
	s.runServer()
	return s.Err
}

func (s *Server) runServer() {
	if s.Running {
		panic("Server Already Running")
	}
	if s.ListenConn == nil {
		panic("Server has no listener")
	}
	s.Running = true
	for {
		conn, err := s.ListenConn.Accept()
		if err != nil {
			s.Err = err
			break
		}
		if tlsConn, ok := conn.(*tls.Conn); ok {
			err = tlsConn.Handshake()
			if err != nil {
				s.Err = err
				conn.Close()
				continue
			}
		}
		go func(c net.Conn) {
			defer c.Close()
			s.handleConnection(c)
		}(conn)
	}
}

func (s *Server) Shutdown() error {
	panic("Not Implemented")
	return nil
}

func MakeClient(addr string, tlsConfig *tls.Config) (*Client, error) {
	client := &Client{}
	client.Lock = &sync.Mutex{}
	client.OpenRequests = make(map[string]chan ResponseType)
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if tlsConfig != nil {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	client.Conn = conn
	client.IdNum = 0
	client.Closed = false
	go client.ReadLoop()
	return client, nil
}

func (c *Client) Close() {
	c.Conn.Close()
	c.Closed = true
}

func marshalJsonNoEnc(val interface{}) ([]byte, error) {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(val)
	if err != nil {
		return nil, err
	}
	return jsonBuf.Bytes(), nil
}

func (c *Client) writePacket(module string, dir string, val interface{}) (string, error) {
	if c.Closed {
		return "", fmt.Errorf("Client closed, cannot request")
	}
	idnum := atomic.AddInt64(&c.IdNum, 1)
	barr, err := marshalJsonNoEnc(val)
	if err != nil {
		return "", err
	}
	ph := PacketHeader{Version: 1, Dir: dir, Id: strconv.FormatInt(idnum, 10), Module: module, Enc: "json", BodyLen: len(barr)}
	if !ph.IsValid() {
		return "", fmt.Errorf("Invalid PacketHeader created")
	}
	headerStr := ph.MakeHeader()
	SetWriteDeadline(c.Conn)
	_, err = c.Conn.Write([]byte(headerStr))
	if err != nil {
		c.Err = err
		c.Close()
		return "", err
	}
	SetWriteDeadline(c.Conn)
	_, err = c.Conn.Write(barr)
	if err != nil {
		c.Err = err
		c.Close()
		return "", err
	}
	return ph.Id, nil
}

func (c *Client) HandlePush(p PushType) {
	c.Lock.Lock()
	pfn := c.PushFn
	c.Lock.Unlock()
	if pfn == nil {
		return
	}
	pfn(p)
	// prtn, err := pfn(p)
	// TODO send response back to server
}

// TODO fix race if someone wants to add to OpenRequets after terminate happens
func (c *Client) TerminateAllOpenRequests() {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	for id, ch := range c.OpenRequests {
		ch <- ResponseType{ConnError: fmt.Errorf("Connection Closed")}
		close(ch)
		delete(c.OpenRequests, id)
	}
}

func (c *Client) ReadLoop() {
	defer c.TerminateAllOpenRequests()
	for {
		var uresp UnifiedResponseType
		respPh, err := readPacket(c.Conn, &uresp)
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			c.Err = err
			c.Close()
			return
		}
		if respPh.Dir == DIR_CALL {
			fmt.Printf("Client cannot process CALL ('CA') packet\n")
			continue
		} else if respPh.Dir == DIR_RTN {
			c.Lock.Lock()
			respCh := c.OpenRequests[respPh.Id]
			if respCh == nil {
				fmt.Printf("got response that does not match an open request %s (ignoring)\n", respPh.Id)
				c.Lock.Unlock()
				continue
			}
			delete(c.OpenRequests, respPh.Id)
			c.Lock.Unlock()

			respVal := uresp.ResponseType
			respCh <- respVal
			close(respCh)
			continue
		} else if respPh.Dir == DIR_PUSH {
			go c.HandlePush(uresp.PushType)
		} else if respPh.Dir == DIR_PUSHREG {
			fmt.Printf("Client cannot process PUSHREG ('RP') packet\n")
			continue
		} else {
			fmt.Printf("Invalid packet direction '%s'\n", respPh.Dir)
			continue
		}
	}
}

func (c *Client) SetPushCtx(pushCtx string, pfn PushFnType) error {
	c.Lock.Lock()
	c.PushCtx = pushCtx
	c.PushFn = pfn
	_, err := c.writePacket("push", DIR_PUSHREG, c.PushCtx)
	c.Lock.Unlock()
	if err != nil {
		c.Err = err
		c.Close()
		return err
	}
	return nil
}

func (c *Client) DoRequest(module string, val interface{}) ResponseType {
	c.Lock.Lock()
	reqId, err := c.writePacket(module, DIR_CALL, val)
	if err != nil {
		c.Lock.Unlock()
		return ResponseType{ConnError: err}
	}
	respCh := make(chan ResponseType, 1)
	c.OpenRequests[reqId] = respCh
	c.Lock.Unlock()

	resp, ok := <-respCh
	if !ok {
		return ResponseType{ConnError: fmt.Errorf("no response received")}
	}
	return resp
}
