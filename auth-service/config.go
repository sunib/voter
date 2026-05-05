package main

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

type config struct {
	Host                           string        `envconfig:"HOST" default:"0.0.0.0"`
	Port                           string        `envconfig:"PORT" default:"8080"`
	CookieSecure                   bool          `envconfig:"COOKIE_SECURE" default:"false"`
	SessionCookieName              string        `envconfig:"SESSION_COOKIE_NAME" default:"auth_session"`
	SessionCookieMaxAgeSecs        int           `envconfig:"SESSION_COOKIE_MAX_AGE_SECONDS" default:"3600"`
	AdminCookieName                string        `envconfig:"ADMIN_COOKIE_NAME" default:"coffee_admin_session"`
	AdminSessionMaxAgeSecs         int           `envconfig:"ADMIN_SESSION_MAX_AGE_SECONDS" default:"28800"`
	AdminPassword                  string        `envconfig:"ADMIN_PASSWORD" default:"testnetcoffee"`
	JoinCodeRotate                 time.Duration `envconfig:"JOIN_CODE_ROTATE_SECONDS" default:"15s"`
	JoinCodeTTL                    time.Duration `envconfig:"JOIN_CODE_TTL_SECONDS" default:"60s"`
	JoinCodeLength                 int           `envconfig:"JOIN_CODE_LENGTH" default:"4"`
	ForwardServiceAccount          string        `envconfig:"FORWARD_SA"`
	ForwardServiceAccountNamespace string        `envconfig:"FORWARD_SA_NAMESPACE"`
	CoffeeConfigName               string        `envconfig:"COFFEE_CONFIG_NAME" default:"testnet-coffee"`
	CoffeeConfigNamespace          string        `envconfig:"COFFEE_CONFIG_NAMESPACE"`
}

func loadConfig() (config, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
