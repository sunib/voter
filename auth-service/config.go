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

	// ConfigButlerGitTargetName names the ConfigButler GitTarget whose open
	// commit window should be finalized after a successful CoffeeConfig patch.
	// Set to "" to disable the save-message side effect entirely.
	ConfigButlerGitTargetName string `envconfig:"CONFIGBUTLER_GIT_TARGET_NAME" default:"voter-demo"`
	// ConfigButlerCommitRequestNamespace overrides where CommitRequest objects
	// are created. Empty means "use the runtime namespace" (the same namespace
	// the auth-service runs in, which must also be the GitTarget's namespace).
	ConfigButlerCommitRequestNamespace string `envconfig:"CONFIGBUTLER_COMMITREQUEST_NAMESPACE"`
	// ConfigButlerIdentityExtrasEnabled controls whether impersonated writes
	// carry the configbutler.ai/claims/{display-name,email} extras. Requires
	// matching RBAC on authentication.k8s.io/userextras/...; flip off if that
	// RBAC is not yet applied to keep the demo working.
	ConfigButlerIdentityExtrasEnabled bool `envconfig:"CONFIGBUTLER_IDENTITY_EXTRAS_ENABLED" default:"true"`
}

func loadConfig() (config, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
