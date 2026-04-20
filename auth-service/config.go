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
	JoinCodeRotate                 time.Duration `envconfig:"JOIN_CODE_ROTATE_SECONDS" default:"15s"`
	JoinCodeTTL                    time.Duration `envconfig:"JOIN_CODE_TTL_SECONDS" default:"60s"`
	JoinCodeLength                 int           `envconfig:"JOIN_CODE_LENGTH" default:"4"`
	ForwardServiceAccount          string        `envconfig:"FORWARD_SA"`
	ForwardServiceAccountNamespace string        `envconfig:"FORWARD_SA_NAMESPACE"`
}

func loadConfig() (config, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
