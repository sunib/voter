package main

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

type config struct {
	Host                           string        `envconfig:"HOST" default:"0.0.0.0"`
	Port                           string        `envconfig:"PORT" default:"8080"`
	CookieSecure                   bool          `envconfig:"COOKIE_SECURE" default:"false"`
	SessionCookieName              string        `envconfig:"SESSION_COOKIE_NAME" default:"demo_session"`
	SessionCookieMaxAgeSecs        int           `envconfig:"SESSION_COOKIE_MAX_AGE_SECONDS" default:"7200"`
	ParticipantCookieName          string        `envconfig:"PARTICIPANT_COOKIE_NAME" default:"participant_session"`
	ParticipantSessionMaxAgeSecs   int           `envconfig:"PARTICIPANT_SESSION_MAX_AGE_SECONDS" default:"43200"`
	AdminCookieName                string        `envconfig:"ADMIN_COOKIE_NAME" default:"coffee_admin_session"`
	AdminSessionMaxAgeSecs         int           `envconfig:"ADMIN_SESSION_MAX_AGE_SECONDS" default:"28800"`
	AdminPassword                  string        `envconfig:"ADMIN_PASSWORD" default:"testnetcoffee"`
	DemoAccessCode                 string        `envconfig:"DEMO_ACCESS_CODE"`
	JoinCodeRotate                 time.Duration `envconfig:"JOIN_CODE_ROTATE_SECONDS" default:"15s"`
	JoinCodeTTL                    time.Duration `envconfig:"JOIN_CODE_TTL_SECONDS" default:"7200s"`
	JoinCodeLength                 int           `envconfig:"JOIN_CODE_LENGTH" default:"4"`
	ForwardServiceAccount          string        `envconfig:"FORWARD_SA"`
	ForwardServiceAccountNamespace string        `envconfig:"FORWARD_SA_NAMESPACE"`
	CoffeeConfigName               string        `envconfig:"COFFEE_CONFIG_NAME" default:"testnet-coffee"`
}

func loadConfig() (config, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
