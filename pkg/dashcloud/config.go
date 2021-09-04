package dashcloud

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
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
	DefaultJwtValidFor     = 24 * time.Hour
	DefaultJwtUserId       = "jwt-user"
	DefaultJwtRole         = dash.RoleUser
)

type Config struct {
	// DASHBORG_ACCID, set to force an AccountId (must match certificate).  If not set, AccountId is set from certificate file.
	// If AccId is given and AutoKeygen is true, and key/cert files are not found, Dashborg will create a new self-signed
	//     keypair using the AccId given.
	// If AccId is given, and the certificate does not match, this will cause a panic.
	AccId string

	// Set to true for unregistered/unclaimed accounts
	AnonAcc bool

	// DASHBORG_ZONE defaults to "default"
	ZoneName string

	// Process Name Attributes.  Only ProcName is required
	ProcName string // DASHBORG_PROCNAME (set from executable filename if not set)
	ProcTags map[string]string

	KeyFileName  string // DASHBORG_KEYFILE private key file (defaults to dashborg-client.key)
	CertFileName string // DASHBORG_CERTFILE certificate file, CN must be set to your Dashborg Account Id.  (defaults to dashborg-client.crt)

	// Create a self-signed key/cert if they do not exist.  This will also create a random Account Id.
	// Should only be used with AnonAcc is true.  If AccId is set, will create a key with that AccId
	AutoKeygen bool

	// DASHBORG_VERBOSE, set to true for extra debugging information
	Verbose bool

	// close this channel to force a shutdown of the Dashborg Cloud Client
	ShutdownCh chan struct{}

	// These are for internal testing, should not normally be set by clients.
	Env                 string // DASHBORG_ENV
	DashborgSrvHost     string // DASHBORG_PROCHOST
	DashborgSrvPort     int    // DASHBORG_PROCPORT
	DashborgConsoleHost string // DASHBORG_CONSOLEHOST

	setupDone bool // internal

	NoShowJWT bool // set to true to disable showing app-link with jwt param
	JWTOpts   *JWTOpts

	Logger *log.Logger // use to override the SDK's logger object
}

var cmdRegexp *regexp.Regexp = regexp.MustCompile("^.*/")

func (c *Config) setDefaults() {
	if c.Logger == nil {
		c.Logger = log.New(os.Stderr, "", log.Ldate|log.Ltime|log.Lmsgprefix)
	}
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
		c.DashborgConsoleHost = dashutil.DefaultString(c.DashborgConsoleHost, os.Getenv("DASHBORG_CONSOLEHOST"), consoleHostDev)
	}
	if c.DashborgSrvPort == 0 {
		if os.Getenv("DASHBORG_PROCPORT") != "" {
			var err error
			c.DashborgSrvPort, err = strconv.Atoi(os.Getenv("DASHBORG_PROCPORT"))
			if err != nil {
				c.log("Invalid DASHBORG_PROCPORT environment variable: %v\n", err)
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

	if c.JWTOpts == nil {
		c.JWTOpts = &JWTOpts{}
	}
	err := c.JWTOpts.ValidateAndSetDefaults()
	if err != nil {
		panic(fmt.Sprintf("Invalid JWTOpts in config: %s", err))
	}
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
	c.AccId = certInfo.AccId
	c.log("DashborgCloudClient KeyFile:%s CertFile:%s AccId:%s SHA-256:%s\n", c.KeyFileName, c.CertFileName, c.AccId, certInfo.Pk256)
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
	c.log("Dashborg created new self-signed keypair %s / %s for new AccId:%s\n", c.KeyFileName, c.CertFileName, accId)
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
	pk256Str := dashutil.Sha256Base64(pubKeyBytes)
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
func (c *Config) MakeAccountJWT(jwtOpts JWTOpts) (string, error) {
	c.setDefaultsAndLoadKeys()
	ecKey, err := c.loadPrivateKey()
	if err != nil {
		return "", err
	}
	err = jwtOpts.ValidateAndSetDefaults()
	if err != nil {
		return "", err
	}
	claims := jwt.MapClaims{}
	claims["iss"] = "dashborg"
	claims["exp"] = time.Now().Add(jwtOpts.ValidFor).Unix()
	claims["iat"] = time.Now().Add(-5 * time.Second).Unix() // skeww
	claims["jti"] = uuid.New().String()
	claims["dash-acc"] = c.AccId
	claims["aud"] = "dashborg-auth"
	claims["sub"] = jwtOpts.UserId
	claims["role"] = jwtOpts.Role
	token := jwt.NewWithClaims(jwt.GetSigningMethod("ES384"), claims)
	jwtStr, err := token.SignedString(ecKey)
	if err != nil {
		return "", fmt.Errorf("Error signing JWT: %w", err)
	}
	return jwtStr, nil
}

func (c *Config) MustMakeAccountJWT(jwtOpts JWTOpts) string {
	rtn, err := c.MakeAccountJWT(jwtOpts)
	if err != nil {
		panic(err)
	}
	return rtn
}

func (c *Config) log(fmtStr string, args ...interface{}) {
	if c.Logger != nil {
		c.Logger.Printf(fmtStr, args...)
	} else {
		log.Printf(fmtStr, args...)
	}
}

func (c *Config) copyJWTOpts() JWTOpts {
	return *c.JWTOpts
}
