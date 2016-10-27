package padlockcloud

import "net/http"
import "net/http/httputil"
import "fmt"
import "encoding/base64"
import "regexp"
import "bytes"
import "strings"
import "time"
import "strconv"
import "path/filepath"
import "gopkg.in/tylerb/graceful.v1"

const (
	ApiVersion = 1
)

func versionFromRequest(r *http.Request) int {
	var vString string
	accept := r.Header.Get("Accept")

	reg := regexp.MustCompile("^application/vnd.padlock;version=(\\d)$")
	if reg.MatchString(accept) {
		vString = reg.FindStringSubmatch(accept)[1]
	} else {
		vString = r.PostFormValue("api_version")
	}

	if vString == "" {
		vString = r.URL.Query().Get("v")
	}

	version, _ := strconv.Atoi(vString)
	return version
}

func getIp(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
}

func formatRequest(r *http.Request) string {
	return fmt.Sprintf("%s %s %s", getIp(r), r.Method, r.URL)
}

func formatRequestVerbose(r *http.Request) string {
	dump, _ := httputil.DumpRequest(r, true)
	return string(dump)
}

// DataStore represents the data associated to a given account
type DataStore struct {
	Account *Account
	Content []byte
}

// Implementation of the `Storable.Key` interface method
func (d *DataStore) Key() []byte {
	return []byte(d.Account.Email)
}

// Implementation of the `Storable.Deserialize` interface method
func (d *DataStore) Deserialize(data []byte) error {
	d.Content = data
	return nil
}

// Implementation of the `Storable.Serialize` interface method
func (d *DataStore) Serialize() ([]byte, error) {
	return d.Content, nil
}

// Server configuration
type ServerConfig struct {
	// Path to assets directory; used for loading templates and such
	AssetsPath string `yaml:"assets_path"`
	// Port to listen on
	Port int `yaml:"port"`
	// Path to TLS certificate
	TLSCert string `yaml:"tls_cert"`
	// Path to TLS key file
	TLSKey string `yaml:"tls_key"`
	// Explicit base url to use in place of http.Request::Host when generating urls and such
	BaseUrl string `yaml:"base_url"`
	// Secret used for authenticating cookies
	Secret string `yaml:"secret"`
}

// The Server type holds all the contextual data and logic used for running a Padlock Cloud instances
// Users should use the `NewServer` function to instantiate an `Server` instance
type Server struct {
	*graceful.Server
	*Log
	Storage            Storage
	Sender             Sender
	Templates          *Templates
	Config             *ServerConfig
	Secure             bool
	Endpoints          map[string]*Endpoint
	secret             []byte
	emailRateLimiter   *EmailRateLimiter
	authRequestCleaner *StorageCleaner
}

func (server *Server) BaseUrl(r *http.Request) string {
	if server.Config.BaseUrl != "" {
		return strings.TrimSuffix(server.Config.BaseUrl, "/")
	} else {
		var scheme string
		if server.Secure {
			scheme = "https"
		} else {
			scheme = "http"
		}
		return fmt.Sprintf("%s://%s", scheme, r.Host)
	}
}

// Retreives Account object from a http.Request object by evaluating the Authorization header and
// cross-checking it with api keys of existing accounts. Returns an `InvalidAuthToken` error
// if no valid Authorization header is provided or if the provided email:api_key pair does not match
// any of the accounts in the database.
func (server *Server) Authenticate(r *http.Request) (*AuthToken, error) {
	authToken, err := AuthTokenFromRequest(r)
	if err != nil {
		return nil, &InvalidAuthToken{}
	}

	invalidErr := &InvalidAuthToken{authToken.Email, authToken.Token}

	acc := &Account{Email: authToken.Email}

	// Fetch account for the given email address
	if err := server.Storage.Get(acc); err != nil {
		if err == ErrNotFound {
			return nil, invalidErr
		} else {
			return nil, err
		}
	}

	// Find the fully populated auth token struct on account. If not found, the value will be nil
	// and we know that the provided token is not valid
	if !authToken.Validate(acc) {
		return nil, invalidErr
	}

	// Check if the token is expired
	if authToken.Expired() {
		return nil, &ExpiredAuthToken{authToken.Email, authToken.Token}
	}

	// If everything checks out, update the `LastUsed` field with the current time
	authToken.LastUsed = time.Now()

	acc.UpdateAuthToken(authToken)

	// Save account info to persist last used data for auth tokens
	if err := server.Storage.Put(acc); err != nil {
		return nil, err
	}

	return authToken, nil
}

func (server *Server) LogError(err error, r *http.Request) {
	switch e := err.(type) {
	case *ServerError, *InvalidCsrfToken:
		server.Error.Printf("%s - %v\nRequest:\n%s\n", formatRequest(r), e, formatRequestVerbose(r))
	default:
		server.Info.Printf("%s - %v", formatRequest(r), e)
	}
}

// Global error handler. Writes a appropriate response to the provided `http.ResponseWriter` object and
// logs / notifies of internal server errors
func (server *Server) HandleError(e error, w http.ResponseWriter, r *http.Request) {
	err, ok := e.(ErrorResponse)

	if !ok {
		err = &ServerError{e}
	}

	server.LogError(err, r)

	var response []byte
	accept := r.Header.Get("Accept")

	if accept == "application/json" || strings.HasPrefix(accept, "application/vnd.padlock") {
		w.Header().Set("Content-Type", "application/json")
		response = JsonifyErrorResponse(err)
	} else if strings.Contains(accept, "text/html") {
		w.Header().Set("Content-Type", "text/html")
		var buff bytes.Buffer
		if err := server.Templates.ErrorPage.Execute(&buff, map[string]string{
			"message": err.Message(),
		}); err != nil {
			server.LogError(&ServerError{err}, r)
		} else {
			response = buff.Bytes()
		}
	}

	if response == nil {
		response = []byte(err.Message())
	}

	w.WriteHeader(err.Status())
	w.Write(response)
}

// Registers handlers mapped by method for a given path
func (server *Server) WrapEndpoint(endpoint *Endpoint) Handler {
	var h Handler = endpoint

	// If auth type is "web", wrap handler in csrf middleware
	if endpoint.AuthType != "" {
		h = (&CSRF{server}).Wrap(h)
	}

	// Check for correct endpoint version
	h = (&CheckEndpointVersion{server, endpoint.Version}).Wrap(h)

	// Wrap handler in auth middleware
	h = (&Authenticate{server, endpoint.AuthType}).Wrap(h)

	// Check if Method is supported
	h = (&CheckMethod{endpoint.Handlers}).Wrap(h)

	h = (&HandlePanic{}).Wrap(h)

	h = (&HandleError{server}).Wrap(h)

	return h
}

// Registeres http handlers for various routes
func (server *Server) InitEndpoints() {
	if server.Endpoints == nil {
		server.Endpoints = make(map[string]*Endpoint)
	}

	// Endpoint for logging in / requesting api keys
	server.Endpoints["/auth/"] = &Endpoint{
		Handlers: map[string]Handler{
			"PUT":  &RequestAuthToken{server},
			"POST": &RequestAuthToken{server},
		},
		Version: ApiVersion,
	}

	// Endpoint for logging in / requesting api keys
	server.Endpoints["/login/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET":  &LoginPage{server},
			"POST": &RequestAuthToken{server},
		},
	}

	// Endpoint for activating auth tokens
	server.Endpoints["/activate/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET": &ActivateAuthToken{server},
		},
	}

	// Endpoint for reading / writing and deleting a store
	server.Endpoints["/store/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET":    &ReadStore{server},
			"HEAD":   &ReadStore{server},
			"PUT":    &WriteStore{server},
			"DELETE": &RequestDeleteStore{server},
		},
		Version:  ApiVersion,
		AuthType: "api",
	}

	server.Endpoints["/deletestore/"] = &Endpoint{
		Handlers: map[string]Handler{
			"POST": &DeleteStore{server},
		},
		AuthType: "web",
	}

	// Dashboard for managing data, auth tokens etc.
	server.Endpoints["/dashboard/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET": &Dashboard{server},
		},
		AuthType: "web",
	}

	// Endpoint for logging out
	server.Endpoints["/logout/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET": &Logout{server},
		},
		AuthType: "web",
	}

	// Endpoint for revoking auth tokens
	server.Endpoints["/revoke/"] = &Endpoint{
		Handlers: map[string]Handler{
			"POST": &Revoke{server},
		},
		AuthType: "web",
	}

	server.Endpoints["/static/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET": NewStaticHandler(
				filepath.Join(server.Config.AssetsPath, "static"),
				"/static/",
			),
		},
	}

	server.Endpoints["/"] = &Endpoint{
		Handlers: map[string]Handler{
			"GET": &RootHandler{server},
			// Older clients might still be using this method. Add a void handler so
			// the request gets past the allowed method check and the request can be handled
			// as a UnsupportedApiVersion error
			"PUT": &VoidHandler{},
		},
	}

}
func (server *Server) InitHandler() {
	mux := http.NewServeMux()

	for key, endpoint := range server.Endpoints {
		mux.Handle(key, HttpHandler(server.WrapEndpoint(endpoint)))
	}

	server.Handler = mux
}

func (server *Server) SendDeprecatedVersionEmail(r *http.Request) error {
	var email string

	// Try getting email from Authorization header first
	if authToken, err := AuthTokenFromRequest(r); err == nil {
		email = authToken.Email
	}

	// Try to extract email from url if method is DELETE
	if email == "" && r.Method == "DELETE" {
		email = r.URL.Path[1:]
	}

	// Try to get email from request body if method is POST
	if email == "" && (r.Method == "PUT" || r.Method == "POST") {
		email = r.PostFormValue("email")
	}

	if email != "" && !server.emailRateLimiter.RateLimit(getIp(r), email) {
		var buff bytes.Buffer
		if err := server.Templates.DeprecatedVersionEmail.Execute(&buff, nil); err != nil {
			return err
		}
		body := buff.String()

		// Send email about deprecated api version
		go func() {
			if err := server.Sender.Send(email, "Please update your version of Padlock", body); err != nil {
				server.LogError(&ServerError{err}, r)
			}
		}()
	}

	return nil
}

func (server *Server) Init() error {
	var err error

	if server.Config.Secret != "" {
		if s, err := base64.StdEncoding.DecodeString(server.Config.Secret); err != nil {
			server.secret = s
		} else {
			return err
		}
	} else {
		if key, err := randomBytes(32); err != nil {
			return err
		} else {
			server.secret = key
		}
	}

	server.InitEndpoints()

	if server.Templates == nil {
		server.Templates = &Templates{}
		// Load templates from assets directory
		if err := LoadTemplates(server.Templates, filepath.Join(server.Config.AssetsPath, "templates")); err != nil {
			return err
		}
	}

	// Open storage
	if err = server.Storage.Open(); err != nil {
		return err
	}

	if rl, err := NewEmailRateLimiter(
		RateQuota{PerMin(1), 5},
		RateQuota{PerMin(1), 5},
	); err != nil {
		return err
	} else {
		server.emailRateLimiter = rl
	}

	// Clean out auth request older than 24hrs once a day
	if cl, err := NewStorageCleaner(server.Storage, &AuthRequest{}, func(t Storable) bool {
		return t.(*AuthRequest).Created.Before(time.Now().Add(-24 * time.Hour))
	}); err != nil {
		return err
	} else {
		server.authRequestCleaner = cl
		cl.Log = server.Log
		cl.Start(24 * time.Hour)
	}

	return nil
}

func (server *Server) CleanUp() error {
	if server.authRequestCleaner != nil {
		server.authRequestCleaner.Stop()
	}
	return server.Storage.Close()
}

func (server *Server) Start() error {
	defer server.CleanUp()

	server.InitHandler()

	port := server.Config.Port
	tlsCert := server.Config.TLSCert
	tlsKey := server.Config.TLSKey

	server.Addr = fmt.Sprintf(":%d", port)

	// Start server
	if tlsCert != "" && tlsKey != "" {
		server.Info.Printf("Starting server with TLS on port %v", port)
		server.Secure = true
		return server.ListenAndServeTLS(tlsCert, tlsKey)
	} else {
		server.Info.Printf("Starting server on port %v", port)
		return server.ListenAndServe()
	}
}

// Instantiates and initializes a new Server and returns a reference to it
func NewServer(log *Log, storage Storage, sender Sender, config *ServerConfig) *Server {
	server := &Server{
		Server: &graceful.Server{
			Server:  &http.Server{},
			Timeout: time.Second * 10,
		},
		Log:     log,
		Storage: storage,
		Sender:  sender,
		Config:  config,
	}

	// Hook up logger for http.Server
	server.ErrorLog = server.Error
	// Hook up logger for graceful.Server
	server.Logger = server.Error

	return server
}

func init() {
	RegisterStorable(&DataStore{}, "data-stores")
}
