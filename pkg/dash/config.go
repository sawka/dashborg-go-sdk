package dash

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/keygen"
)

const (
	TLS_KEY_FILENAME         = "dashborg-client.key"
	TLS_CERT_FILENAME        = "dashborg-client.crt"
	DEFAULT_PROCNAME         = "default"
	DEFAULT_ZONENAME         = "default"
	DEFAULT_PANELNAME        = "default"
	DEFAULT_LOCALSERVER_ADDR = "localhost:8082"
)

var cmdRegexp *regexp.Regexp = regexp.MustCompile("^.*/")

func defaultString(opts ...string) string {
	for _, s := range opts {
		if s != "" {
			return s
		}
	}
	return ""
}

func envOverride(val bool, varName string) bool {
	envVal := os.Getenv(varName)
	if envVal == "0" {
		return false
	}
	if envVal == "" {
		return val
	}
	return true
}

func (c *Config) setDefaults() {
	c.AccId = defaultString(c.AccId, os.Getenv("DASHBORG_ACCID"))
	c.ZoneName = defaultString(c.ZoneName, os.Getenv("DASHBORG_ZONE"), DEFAULT_ZONENAME)
	c.Env = defaultString(c.Env, os.Getenv("DASHBORG_ENV"), "prod")
	if c.Env == "prod" {
		c.DashborgSrvHost = defaultString(c.DashborgSrvHost, os.Getenv("DASHBORG_PROCHOST"), "grpc.api.dashborg.net")
	} else {
		c.DashborgSrvHost = defaultString(c.DashborgSrvHost, os.Getenv("DASHBORG_PROCHOST"), "localhost")
	}
	if c.DashborgSrvPort == 0 {
		if os.Getenv("DASHBORG_PROCPORT") != "" {
			var err error
			c.DashborgSrvPort, err = strconv.Atoi(os.Getenv("DASHBORG_PROCPORT"))
			if err != nil {
				log.Printf("Invalid DASHBORG_PROCPORT environment variable: %v\n", err)
			}
		}
		if c.DashborgSrvPort == 0 {
			c.DashborgSrvPort = 7632
		}
	}
	var cmdName string
	if len(os.Args) > 0 {
		cmdName = cmdRegexp.ReplaceAllString(os.Args[0], "")
	}
	c.ProcName = defaultString(c.ProcName, os.Getenv("DASHBORG_PROCNAME"), cmdName, DEFAULT_PROCNAME)
	c.KeyFileName = defaultString(c.KeyFileName, os.Getenv("DASHBORG_KEYFILE"), TLS_KEY_FILENAME)
	c.CertFileName = defaultString(c.CertFileName, os.Getenv("DASHBORG_CERTFILE"), TLS_CERT_FILENAME)
	c.Verbose = envOverride(c.Verbose, "DASHBORG_VERBOSE")
	if c.MinClearTimeout == 0 {
		c.MinClearTimeout = 1 * time.Second
	}
	c.LocalServer = envOverride(c.LocalServer, "DASHBORG_LOCALSERVER")
	if c.LocalServer {
		envLspn := os.Getenv("DASHBORG_LOCALSERVER")
		if !dashutil.IsPanelNameValid(envLspn) {
			envLspn = ""
		}
		c.LocalServerPanelName = defaultString(c.LocalServerPanelName, os.Getenv("DASHBORG_LOCALSERVERPANELNAME"), envLspn, DEFAULT_PANELNAME)
		c.LocalServerAddr = defaultString(c.LocalServerAddr, os.Getenv("DASHBORG_LOCALSERVERADDR"), DEFAULT_LOCALSERVER_ADDR)
	}
}

func (c *Config) setupForProcClient() {
	c.setDefaults()
	c.loadKeys()
}

func (c *Config) loadKeys() {
	if c.AutoKeygen {
		err := c.maybeMakeKeys(c.AccId)
		if err != nil {
			panic(err)
		}
	}
	if _, errKey := os.Stat(c.KeyFileName); os.IsNotExist(errKey) {
		panic(fmt.Sprintf("Dashborg key file does not exist file:%s", c.KeyFileName))
	}
	if _, errCert := os.Stat(c.CertFileName); os.IsNotExist(errCert) {
		panic(fmt.Sprintf("Dashborg certificate file does not exist file:%s", c.CertFileName))
	}
	certInfo, err := readCertInfo(c.CertFileName)
	if err != nil {
		panic(err)
	}
	if c.AccId != "" && certInfo.AccId != c.AccId {
		panic(fmt.Sprintf("Dashborg AccId read from certificate:%s does not match AccId in config:%s", certInfo.AccId, c.AccId))
	}
	log.Printf("Dashborg KeyFile:%s CertFile:%s SHA256:%s\n", c.KeyFileName, c.CertFileName, certInfo.Pk256)
	c.AccId = certInfo.AccId
}

func (c *Config) maybeMakeKeys(accId string) error {
	if c.KeyFileName == "" || c.CertFileName == "" {
		return fmt.Errorf("Empty/Invalid Key or Cert filenames")
	}
	_, errKey := os.Stat(c.KeyFileName)
	_, errCert := os.Stat(c.CertFileName)
	if errKey == nil && errCert == nil {
		return nil
	}
	if errKey == nil || errCert == nil {
		return fmt.Errorf("Cannot make key:%s cert:%s, one or both files already exist", c.KeyFileName, c.CertFileName)
	}
	if accId == "" {
		accId = uuid.New().String()
	}
	err := keygen.CreateKeyPair(c.KeyFileName, c.CertFileName, accId)
	if err != nil {
		return fmt.Errorf("Cannot create keypair err:%v", err)
	}
	log.Printf("Dashborg created new self-signed keypair key:%s cert:%s for new accountid:%s\n", c.KeyFileName, c.CertFileName, accId)
	return nil
}

type certInfo struct {
	AccId string
	Pk256 string
}

func readCertInfo(certFileName string) (*certInfo, error) {
	certBytes, err := ioutil.ReadFile(certFileName)
	if err != nil {
		return nil, fmt.Errorf("Cannot read certificate file:%s err:%w", certFileName, err)
	}
	block, _ := pem.Decode(certBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("Certificate file malformed, failed to Decode PEM CERTIFICATE block from file:%s", certFileName)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert == nil {
		return nil, fmt.Errorf("Error parsing certificate from file:%s err:%w", certFileName, err)
	}
	cn := cert.Subject.CommonName
	if cn == "" || !dashutil.IsUUIDValid(cn) {
		return nil, fmt.Errorf("Invalid CN in certificate.  CN should be set to Dashborg Account ID (UUID formatted, 36 chars) CN:%s", cn)
	}
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("Cannot get PublicKey bytes from certificate")
	}
	pk256 := sha256.Sum256(pubKeyBytes)
	pk256Str := base64.StdEncoding.EncodeToString(pk256[:])
	return &certInfo{AccId: cn, Pk256: pk256Str}, nil
}
