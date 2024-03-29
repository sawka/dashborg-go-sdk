// Structured errors for Dashborg providing a wrapped error with error codes.
package dasherr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
)

type ErrCode string

const (
	ErrCodeNone         ErrCode = ""
	ErrCodeEof          ErrCode = "EOF"
	ErrCodeUnknown      ErrCode = "UNKNOWN"
	ErrCodeBadConnId    ErrCode = "BADCONNID"
	ErrCodeAccAccess    ErrCode = "ACCACCESS"
	ErrCodeNoHandler    ErrCode = "NOHANDLER"
	ErrCodeBadAuth      ErrCode = "BADAUTH"
	ErrCodeRoleAuth     ErrCode = "BADROLE"
	ErrCodeBadZone      ErrCode = "BADZONE"
	ErrCodeNoAcc        ErrCode = "NOACC"
	ErrCodeOffline      ErrCode = "OFFLINE"
	ErrCodePanic        ErrCode = "PANIC"
	ErrCodeJson         ErrCode = "JSON"
	ErrCodeRpc          ErrCode = "RPC"
	ErrCodeUpload       ErrCode = "UPLOAD"
	ErrCodeLimit        ErrCode = "LIMIT"
	ErrCodeNotConnected ErrCode = "NOCONN"
	ErrCodeValidation   ErrCode = "NOTVALID"
	ErrCodeQueueFull    ErrCode = "QUEUE"
	ErrCodeTimeout      ErrCode = "TIMEOUT"
	ErrCodeNotImpl      ErrCode = "NOTIMPL"
	ErrCodePathNotFound ErrCode = "NOTFOUND"
	ErrCodeBadPath      ErrCode = "BADPATH"
	ErrCodeNoApp        ErrCode = "NOAPP"
	ErrCodeProtocol     ErrCode = "PROTOCOL"
	ErrCodeInitErr      ErrCode = "INITERR"
)

type DashErr struct {
	apiName   string
	err       error
	code      ErrCode
	permanent bool
}

func (e *DashErr) Error() string {
	codeStr := ""
	if e.code != "" {
		codeStr = fmt.Sprintf("[%s] ", e.code)
	}
	if e.apiName == "" {
		return fmt.Sprintf("%s%v", codeStr, e.err)
	}
	return fmt.Sprintf("Error calling %s: %s%v", e.apiName, codeStr, e.err)
}

func (e *DashErr) Unwrap() error {
	return e.err
}

func (e *DashErr) ErrCode() ErrCode {
	return e.code
}

func (e *DashErr) CanRetry() bool {
	return !e.permanent
}

// If err is a DashErr, returns CanRetry(), otherwise returns true.
func CanRetry(err error) bool {
	var dashErr *DashErr
	if errors.As(err, &dashErr) {
		return dashErr.CanRetry()
	}
	return true
}

// If err is a DashErr, unwraps it to return the inner error message, otherwise
// just calls Error().
func GetMessage(err error) string {
	var dashErr *DashErr
	if errors.As(err, &dashErr) {
		return dashErr.err.Error()
	}
	return err.Error()
}

// If err is a DashErr returns the error code, otherwise returns "" for non-DashErr errors.
func GetErrCode(err error) ErrCode {
	var dashErr *DashErr
	if errors.As(err, &dashErr) {
		return dashErr.ErrCode()
	}
	return ErrCodeNone
}

// If err is a DashErr, returns it, otherwise wraps it into a DashErr struct.
func AsDashErr(err error) *DashErr {
	var dashErr *DashErr
	if errors.As(err, &dashErr) {
		return dashErr
	}
	return &DashErr{err: err}
}

// Wraps err into a DashErr with the given error code
func ErrWithCode(code ErrCode, err error) error {
	return &DashErr{err: err, code: code}
}

// Wraps errStr into a DashErr with the given error code
func ErrWithCodeStr(code ErrCode, errStr string) error {
	return &DashErr{err: errors.New(errStr), code: code}
}

// Wraps err into a DashErr with no-retry set.
func NoRetryErr(err error) error {
	return &DashErr{err: err, permanent: true}
}

// Wraps err into a DashErr with the given error code and no-retry set.
func NoRetryErrWithCode(code ErrCode, err error) error {
	return &DashErr{code: code, err: err, permanent: true}
}

// Creates a DashErr
func MakeDashErr(code ErrCode, isPermanent bool, err error) error {
	return &DashErr{code: code, err: err, permanent: isPermanent}
}

// Creates a DashErr from a gRPC RtnStatus
func FromRtnStatus(apiName string, rtnStatus *dashproto.RtnStatus) error {
	if rtnStatus == nil {
		return &DashErr{apiName: apiName, err: errors.New("No Return Status"), permanent: true}
	}
	if rtnStatus.Success {
		return nil
	}
	var statusErr error
	if rtnStatus.Err != "" {
		statusErr = errors.New(rtnStatus.Err)
	} else {
		statusErr = errors.New("Unspecified Error")
	}
	rtnErr := &DashErr{
		apiName:   apiName,
		err:       statusErr,
		code:      ErrCode(rtnStatus.ErrCode),
		permanent: rtnStatus.PermErr,
	}
	return rtnErr
}

// Creates a dashproto.ErrorType from the fields of DashErr
func AsProtoErr(err error) *dashproto.ErrorType {
	if err == nil {
		return nil
	}
	return &dashproto.ErrorType{
		Err:     GetMessage(err),
		ErrCode: string(GetErrCode(err)),
		PermErr: !CanRetry(err),
	}
}

// Turns a dashproto.ErrorType into a DashErr
func FromProtoErr(perr *dashproto.ErrorType) error {
	if perr == nil {
		return nil
	}
	var statusErr error
	if perr.Err != "" {
		statusErr = errors.New(perr.Err)
	} else {
		statusErr = errors.New("Unspecified Error")
	}
	return &DashErr{
		err:       statusErr,
		code:      ErrCode(perr.ErrCode),
		permanent: perr.PermErr,
	}
}

// Creates a DashErr from a gRPC error
func RpcErr(apiName string, respErr error) error {
	if respErr == nil {
		return nil
	}
	rtnErr := &DashErr{
		apiName: apiName,
		err:     respErr,
		code:    ErrCodeRpc,
	}
	return rtnErr
}

// Creates a DashErr from a json.Marshal error
func JsonMarshalErr(thing string, err error) error {
	return &DashErr{
		err:       fmt.Errorf("Error Marshaling %s to JSON: %w", thing, err),
		code:      ErrCodeJson,
		permanent: true,
	}
}

// Creates a DashErr from a json.Unmarshal error
func JsonUnmarshalErr(thing string, err error) error {
	return &DashErr{
		err:       fmt.Errorf("Error Unmarshaling %s from JSON: %w", thing, err),
		code:      ErrCodeJson,
		permanent: true,
	}
}

// Creates a validation DashErr
func ValidateErr(err error) error {
	if GetErrCode(err) == ErrCodeValidation {
		return err
	}
	return &DashErr{
		err:       err,
		code:      ErrCodeValidation,
		permanent: true,
	}
}

// Creates a DashErr around an exceeded account limit.
func LimitErr(message string, limitName string, limitMax float64) error {
	limitUnit := ""
	if strings.HasSuffix(limitName, "MB") {
		limitUnit = "MB"
	}
	return &DashErr{
		err:       fmt.Errorf("DashborgLimitError limit:%s exceeded, max=%0.1f%s - %s", limitName, limitMax, limitUnit, message),
		code:      ErrCodeLimit,
		permanent: true,
	}
}
