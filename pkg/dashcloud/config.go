package dashcloud

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
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

	"github.com/dgrijalva/jwt-go"
	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/keygen"
)

const (
	TlsKeyFileName         = "dashborg-client.key"
	TlsCertFileName        = "dashborg-client.crt"
	DefaultProcName        = "default"
	DefaultZoneName        = "default"
	DefaultPanelName       = "default"
	DefaultLocalServerAddr = "localhost:8082"
	DefaultConsoleHost     = "console.dashborg.net"
	DefaultProcHost        = "grpc.api.dashborg.net"
)

var cmdRegexp *regexp.Regexp = regexp.MustCompile("^.*/")

func (c *Config) setDefaults() {
	c.AccId = dashutil.DefaultString(c.AccId, os.Getenv("DASHBORG_ACCID"))
	c.ZoneName = dashutil.DefaultString(c.ZoneName, os.Getenv("DASHBORG_ZONE"), DefaultZoneName)
	c.Env = dashutil.DefaultString(c.Env, os.Getenv("DASHBORG_ENV"), "prod")
	if c.Env == "prod" {
		c.DashborgSrvHost = dashutil.DefaultString(c.DashborgSrvHost, os.Getenv("DASHBORG_PROCHOST"), "")
	} else {
		c.DashborgSrvHost = dashutil.DefaultString(c.DashborgSrvHost, os.Getenv("DASHBORG_PROCHOST"), "")
	}
	if c.Env == "prod" {
		c.DashborgConsoleHost = dashutil.DefaultString(c.DashborgConsoleHost, os.Getenv("DASHBORG_CONSOLEHOST"), DefaultConsoleHost)
	} else {
		c.DashborgConsoleHost = dashutil.DefaultString(c.DashborgConsoleHost, os.Getenv("DASHBORG_CONSOLEHOST"), "console.dashborg-dev.com:8080")
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
	c.ProcName = dashutil.DefaultString(c.ProcName, os.Getenv("DASHBORG_PROCNAME"), cmdName, DefaultProcName)
	c.KeyFileName = dashutil.DefaultString(c.KeyFileName, os.Getenv("DASHBORG_KEYFILE"), TlsKeyFileName)
	c.CertFileName = dashutil.DefaultString(c.CertFileName, os.Getenv("DASHBORG_CERTFILE"), TlsCertFileName)
	c.Verbose = dashutil.EnvOverride(c.Verbose, "DASHBORG_VERBOSE")
}

func (c *Config) setDefaultsAndLoadKeys() {
	if !c.setupDone {
		c.setDefaults()
		c.loadKeys()
		c.setupDone = true
	}
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
	log.Printf("Dashborg created new self-signed keypair %s / %s for new AccId:%s\n", c.KeyFileName, c.CertFileName, accId)
	return nil
}

type certInfo struct {
	AccId     string
	Pk256     string
	PublicKey interface{}
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
	return &certInfo{AccId: cn, Pk256: pk256Str, PublicKey: cert.PublicKey}, nil
}

func (c *Config) loadPrivateKey() (interface{}, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFileName, c.KeyFileName)
	if err != nil {
		return nil, fmt.Errorf("Error loading x509 key pair cert[%s] key[%s]: %w", c.CertFileName, c.KeyFileName, err)
	}
	ecKey, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("Invalid private key %s, must be ECDSA", c.KeyFileName)
	}
	return ecKey, nil
}

// Creates a JWT token from the public/private keypair
func (c *Config) MakeAccountJWT(validFor time.Duration, id string, role string) (string, error) {
	c.setDefaultsAndLoadKeys()
	ecKey, err := c.loadPrivateKey()
	if err != nil {
		return "", err
	}
	claims := jwt.MapClaims{}
	claims["iss"] = "dashborg"
	claims["exp"] = time.Now().Add(validFor).Unix()
	claims["iat"] = time.Now().Add(-5 * time.Second).Unix() // skeww
	claims["jti"] = uuid.New().String()
	claims["dash-acc"] = c.AccId
	if id != "" {
		claims["aud"] = "dashborg-auth"
		claims["sub"] = id
		if role == "" {
			role = "user"
		}
		claims["role"] = role

	} else {
		claims["aud"] = "dashborg-csrf"
	}
	token := jwt.NewWithClaims(jwt.GetSigningMethod("ES384"), claims)
	jwtStr, err := token.SignedString(ecKey)
	if err != nil {
		return "", fmt.Errorf("Error signing JWT: %w", err)
	}
	return jwtStr, nil
}

func (c *Config) MustMakeAccountJWT(validFor time.Duration, id string, role string) string {
	rtn, err := c.MakeAccountJWT(validFor, id, role)
	if err != nil {
		panic(err)
	}
	return rtn
}

func (c *Config) appLink(appName string) string {
	accId := c.AccId
	zoneName := c.ZoneName
	if c.Env != "prod" {
		return fmt.Sprintf("https://acc-%s.console.dashborg-dev.com:8080/zone/%s/%s", accId, zoneName, appName)
	}
	return fmt.Sprintf("https://acc-%s.console.dashborg.net/zone/%s/%s", accId, zoneName, appName)
}

func (c *Config) MakeJWTAppLink(appName string, validTime time.Duration, userId string, roleName string) (string, error) {
	if validTime == 0 {
		validTime = 24 * time.Hour
	}
	if roleName == "" {
		roleName = "user"
	}
	if userId == "" {
		userId = "jwt-user"
	}
	jwtToken, err := c.MakeAccountJWT(validTime, userId, roleName)
	link := c.appLink(appName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s?jwt=%s", link, jwtToken), nil
}

func (c *Config) MustMakeJWTAppLink(appName string, validTime time.Duration, userId string, roleName string) string {
	rtn, err := c.MakeJWTAppLink(appName, validTime, userId, roleName)
	if err != nil {
		panic(err)
	}
	return rtn
}
