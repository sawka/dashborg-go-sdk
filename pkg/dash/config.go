package dash

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/mfmt/pkg/di"
	"github.com/sawka/mfmt/pkg/keygen"
)

const (
	TLS_KEY_FILENAME  = "dashborg-client.key"
	TLS_CERT_FILENAME = "dashborg-client.crt"
	DEFAULT_PROCNAME  = "default"
	DEFAULT_ZONENAME  = "default"
	DEFAULT_PANELNAME = "default"
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

func (c *Config) SetDefaults() {
	c.AccId = defaultString(c.AccId, os.Getenv("DASHBORG_ACCID"))
	c.ZoneName = defaultString(c.ZoneName, os.Getenv("DASHBORG_ZONE"), DEFAULT_ZONENAME)
	c.Env = defaultString(c.Env, os.Getenv("DASHBORG_ENV"), "prod")
	if c.Env == "prod" {
		c.BufSrvHost = defaultString(c.BufSrvHost, os.Getenv("DASHBORG_PROCHOST"), "proc.api.dashborg.net")
	} else {
		c.BufSrvHost = defaultString(c.BufSrvHost, os.Getenv("DASHBORG_PROCHOST"), "localhost")
	}
	if c.BufSrvPort == 0 {
		if os.Getenv("DASHBORG_PROC_PORT") != "" {
			var err error
			c.BufSrvPort, err = strconv.Atoi(os.Getenv("DASHBORG_PROCPORT"))
			if err != nil {
				log.Printf("Invalid DASHBORG_PROC_PORT environment variable: %v\n", err)
			}
		}
		if c.BufSrvPort == 0 {
			c.BufSrvPort = 7533
		}
	}
	var cmdName string
	if len(os.Args) > 0 {
		cmdName = cmdRegexp.ReplaceAllString(os.Args[0], "")
	}
	c.ProcName = defaultString(c.ProcName, os.Getenv("DASHBORG_PROCNAME"), cmdName, DEFAULT_PROCNAME)
	c.ProcINum = 0
	c.KeyFileName = defaultString(c.KeyFileName, os.Getenv("DASHBORG_KEYFILE"), TLS_KEY_FILENAME)
	c.CertFileName = defaultString(c.CertFileName, os.Getenv("DASHBORG_CERTFILE"), TLS_CERT_FILENAME)
	if os.Getenv("DASHBORG_VERBOSE") != "" {
		c.Verbose = true
	}
	if c.MinClearTimeout == 0 {
		c.MinClearTimeout = 1 * time.Second
	}
}

func (c *Config) UseAnonKeys() {
	c.UseKeys(TLS_KEY_FILENAME, TLS_CERT_FILENAME, true)
}

func (c *Config) UseKeys(keyFileName string, certFileName string, autoCreate bool) {
	c.KeyFileName = keyFileName
	c.CertFileName = certFileName
	if autoCreate {
		err := c.maybeMakeKeys()
		if err != nil {
			panic(err)
		}
		c.AutoKeygen = true
	}
	if _, errKey := os.Stat(c.KeyFileName); os.IsNotExist(errKey) {
		panic(fmt.Sprintf("MFMT key file does not exist file:%s", c.KeyFileName))
	}
	if _, errCert := os.Stat(c.CertFileName); os.IsNotExist(errCert) {
		panic(fmt.Sprintf("MFMT certificate file does not exist file:%s", c.CertFileName))
	}
	accId, err := readAccIdFromCert(certFileName)
	if err != nil {
		panic(err)
	}
	c.AccId = accId
}

func (c *Config) maybeMakeKeys() error {
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
	accId := uuid.New().String()
	err := keygen.CreateKeyPair(c.KeyFileName, c.CertFileName, accId)
	if err != nil {
		return fmt.Errorf("Cannot create keypair err:%v", err)
	}
	log.Printf("MFMT Created new self-signed keypair key:%s cert:%s for new accountid:%s\n", c.KeyFileName, c.CertFileName, accId)
	return nil
}

func readAccIdFromCert(certFileName string) (string, error) {
	certBytes, err := ioutil.ReadFile(certFileName)
	if err != nil {
		return "", fmt.Errorf("Cannot read certificate file:%s err:%w", certFileName, err)
	}
	block, _ := pem.Decode(certBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("Certificate file malformed, failed to Decode PEM CERTIFICATE block from file:%s", certFileName)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert == nil {
		return "", fmt.Errorf("Error parsing certificate from file:%s err:%w", certFileName, err)
	}
	cn := cert.Subject.CommonName
	if cn == "" || !di.IsUUIDValid(cn) {
		return "", fmt.Errorf("Invalid CN in certificate.  CN should be set to MFMT Account ID (UUID formatted, 36 chars) CN:%s", cn)
	}
	return cn, nil
}