package padlockcloud

import "fmt"
import "path/filepath"
import "io/ioutil"
import "errors"
import "encoding/base64"
import "gopkg.in/yaml.v2"
import "gopkg.in/urfave/cli.v1"

type CliConfig struct {
	Log     LogConfig     `yaml:"log"`
	Server  ServerConfig  `yaml:"server"`
	LevelDB LevelDBConfig `yaml:"leveldb"`
	Email   EmailConfig   `yaml:"email"`
}

func (c *CliConfig) LoadFromFile(path string) error {
	// load config file
	yamlData, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(yamlData, c)
	if err != nil {
		return err
	}

	return nil
}

type CliApp struct {
	*cli.App
	*Log
	Storage    *LevelDBStorage
	Email      *EmailSender
	Server     *Server
	Config     *CliConfig
	ConfigPath string
}

func (cliApp *CliApp) InitConfig() {
	cliApp.Config = &CliConfig{}
	cliApp.Log.Config = &cliApp.Config.Log
	cliApp.Storage.Config = &cliApp.Config.LevelDB
	cliApp.Email.Config = &cliApp.Config.Email
	cliApp.Server.Config = &cliApp.Config.Server
}

func (cliApp *CliApp) RunServer(context *cli.Context) error {
	cfg, _ := yaml.Marshal(cliApp.Config)
	cliApp.Server.Info.Printf("Running server with the following configuration:\n%s", cfg)

	if cliApp.Config.Server.Host == "" {
		fmt.Printf("\nWARNING: No --host option provided for generating urls. The 'Host' header from\n" +
			"incoming requests will be used instead. Note that the 'Host' header can easily be\n" +
			"spoofed! Unless you're running this server behind a reverse proxy you probably want\n" +
			"to provide an explicit host string! See the README for details.\n\n")
	}

	return cliApp.Server.Start()
}

func (cliApp *CliApp) ListAccounts(context *cli.Context) error {
	if err := cliApp.Storage.Open(); err != nil {
		return err
	}
	defer cliApp.Storage.Close()

	var acc *Account
	accs, err := cliApp.Storage.List(acc)
	if err != nil {
		return err
	}

	if len(accs) == 0 {
		fmt.Println("No existing accounts!")
	} else {
		output := ""
		for _, email := range accs {
			output = output + email + "\n"
		}
		fmt.Print(output)
	}
	return nil
}

func (cliApp *CliApp) CreateAccount(context *cli.Context) error {
	email := context.Args().Get(0)
	if email == "" {
		return errors.New("Please provide an email address!")
	}
	acc := &Account{
		Email: email,
	}

	if err := cliApp.Storage.Open(); err != nil {
		return err
	}
	defer cliApp.Storage.Close()

	if err := cliApp.Storage.Put(acc); err != nil {
		return err
	}
	return nil
}

func (cliApp *CliApp) DisplayAccount(context *cli.Context) error {
	email := context.Args().Get(0)
	if email == "" {
		return errors.New("Please provide an email address!")
	}
	acc := &Account{
		Email: email,
	}

	if err := cliApp.Storage.Open(); err != nil {
		return err
	}
	defer cliApp.Storage.Close()

	if err := cliApp.Storage.Get(acc); err != nil {
		return err
	}

	yamlData, err := yaml.Marshal(acc)
	if err != nil {
		return err
	}

	fmt.Println(string(yamlData))

	return nil
}

func (cliApp *CliApp) DeleteAccount(context *cli.Context) error {
	email := context.Args().Get(0)
	if email == "" {
		return errors.New("Please provide an email address!")
	}
	acc := &Account{Email: email}

	if err := cliApp.Storage.Open(); err != nil {
		return err
	}
	defer cliApp.Storage.Close()

	return cliApp.Storage.Delete(acc)
}

func genSecret() (string, error) {
	b, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (cliApp *CliApp) GenSecret(context *cli.Context) error {
	s, err := genSecret()
	if err != nil {
		return err
	}
	fmt.Println(s)
	return nil
}

func NewCliApp() *CliApp {
	storage := &LevelDBStorage{}
	email := &EmailSender{}
	logger := &Log{
		Sender: email,
	}
	server := NewServer(
		logger,
		storage,
		email,
		nil,
	)
	cliApp := &CliApp{
		App:     cli.NewApp(),
		Log:     logger,
		Storage: storage,
		Email:   email,
		Server:  server,
	}
	cliApp.InitConfig()
	config := cliApp.Config

	cliApp.Name = "padlock-cloud"
	cliApp.Version = Version
	cliApp.Usage = "A command line interface for Padlock Cloud"

	cliApp.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "config, c",
			Value:       "",
			Usage:       "Path to configuration file. WARNING: If provided, all other flags will be ingored",
			EnvVar:      "PC_CONFIG_PATH",
			Destination: &cliApp.ConfigPath,
		},
		cli.StringFlag{
			Name:        "log-file",
			Value:       "",
			Usage:       "Path to log file",
			EnvVar:      "PC_LOG_FILE",
			Destination: &config.Log.LogFile,
		},
		cli.StringFlag{
			Name:        "err-file",
			Value:       "",
			Usage:       "Path to error log file",
			EnvVar:      "PC_ERR_FILE",
			Destination: &config.Log.ErrFile,
		},
		cli.StringFlag{
			Name:        "notify-errors",
			Usage:       "Email address to send unexpected errors to",
			Value:       "",
			EnvVar:      "PC_NOTIFY_ERRORS",
			Destination: &config.Log.NotifyErrors,
		},
		cli.StringFlag{
			Name:        "db-path",
			Value:       "db",
			Usage:       "Path to LevelDB database",
			EnvVar:      "PC_LEVELDB_PATH",
			Destination: &config.LevelDB.Path,
		},
		cli.StringFlag{
			Name:        "email-server",
			Value:       "",
			Usage:       "Mail server for sending emails",
			EnvVar:      "PC_EMAIL_SERVER",
			Destination: &config.Email.Server,
		},
		cli.StringFlag{
			Name:        "email-port",
			Value:       "",
			Usage:       "Port to use with mail server",
			EnvVar:      "PC_EMAIL_PORT",
			Destination: &config.Email.Port,
		},
		cli.StringFlag{
			Name:        "email-user",
			Value:       "",
			Usage:       "Username for authentication with mail server",
			EnvVar:      "PC_EMAIL_USER",
			Destination: &config.Email.User,
		},
		cli.StringFlag{
			Name:        "email-password",
			Value:       "",
			Usage:       "Password for authentication with mail server",
			EnvVar:      "PC_EMAIL_PASSWORD",
			Destination: &config.Email.Password,
		},
	}

	cliApp.Commands = []cli.Command{
		{
			Name:  "runserver",
			Usage: "Starts a Padlock Cloud server instance",
			Flags: []cli.Flag{
				cli.IntFlag{
					Name:        "port, p",
					Usage:       "Port to listen on",
					Value:       3000,
					EnvVar:      "PC_PORT",
					Destination: &config.Server.Port,
				},
				cli.StringFlag{
					Name:        "assets-path",
					Usage:       "Path to assets directory",
					Value:       DefaultAssetsPath,
					EnvVar:      "PC_ASSETS_PATH",
					Destination: &config.Server.AssetsPath,
				},
				cli.StringFlag{
					Name:        "tls-cert",
					Usage:       "Path to TLS certification file",
					Value:       "",
					EnvVar:      "PC_TLS_CERT",
					Destination: &config.Server.TLSCert,
				},
				cli.StringFlag{
					Name:        "tls-key",
					Usage:       "Path to TLS key file",
					Value:       "",
					EnvVar:      "PC_TLS_KEY",
					Destination: &config.Server.TLSKey,
				},
				cli.StringFlag{
					Name: "host",
					Usage: "Host string to used for generating urls. May include the port. " +
						"If not provided the requests 'Host' header is used.",
					Value:       "",
					EnvVar:      "PC_HOST",
					Destination: &config.Server.Host,
				},
			},
			Action: cliApp.RunServer,
		},
		{
			Name:  "accounts",
			Usage: "Commands for managing accounts",
			Subcommands: []cli.Command{
				{
					Name:   "list",
					Usage:  "List existing accounts",
					Action: cliApp.ListAccounts,
				},
				{
					Name:   "create",
					Usage:  "Create new account",
					Action: cliApp.CreateAccount,
				},
				{
					Name:   "display",
					Usage:  "Display account",
					Action: cliApp.DisplayAccount,
				},
				{
					Name:   "delete",
					Usage:  "Delete account",
					Action: cliApp.DeleteAccount,
				},
			},
		},
		{
			Name:   "gensecret",
			Usage:  "Generate random 32 byte secret",
			Action: cliApp.GenSecret,
		},
	}

	cliApp.Before = func(context *cli.Context) error {
		if cliApp.ConfigPath != "" {
			absPath, _ := filepath.Abs(cliApp.ConfigPath)

			fmt.Printf("Loading config from %s - all other flags and environment variables will be ignored!\n", absPath)
			// Replace original config object to prevent flags from being applied
			cliApp.InitConfig()
			err := cliApp.Config.LoadFromFile(cliApp.ConfigPath)
			if err != nil {
				return err
			}
		}

		// Reinitializing log since log config may have changed
		if err := cliApp.Log.Init(); err != nil {
			return err
		}

		return nil
	}

	return cliApp
}
