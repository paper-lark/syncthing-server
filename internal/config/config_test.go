package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"syncthing-server/internal/config"
)

func TestLoadDefaultsRegistrationAndKeepsSecretsSeparate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	data := `{"baseURL":"http://localhost:8080","oidcIssuer":"https://id.example.test","oidcClientID":"sync","storageRoot":"/data","databasePath":"/db/app.sqlite","syncthingURL":"http://127.0.0.1:8384"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("APP_SYNCTHING_API_KEY", "api-secret")
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RegistrationEnabled || got.Listen != ":8080" || got.SyncthingMajor != 2 {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	if got.OIDCClientSecret != "client-secret" || got.SyncthingAPIKey != "api-secret" {
		t.Fatal("secret not read from environment")
	}
}

func TestLoadRejectsSecretInConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	data := `{"baseURL":"http://localhost:8080","oidcIssuer":"https://id.example.test","oidcClientID":"sync","storageRoot":"/data","databasePath":"/db/app.sqlite","syncthingURL":"http://127.0.0.1:8384","oidcClientSecret":"leaked"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("APP_SYNCTHING_API_KEY", "api-secret")
	if _, err := config.Load(path); err == nil {
		t.Fatal("accepted secret in configuration file")
	}
}

func TestLoadRejectsNonLoopbackSyncthingAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	data := `{"baseURL":"https://sync.example.test","oidcIssuer":"https://id.example.test","oidcClientID":"sync","storageRoot":"/data","databasePath":"/db/app.sqlite","syncthingURL":"http://syncthing.namespace:8384"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("APP_SYNCTHING_API_KEY", "api-secret")
	if _, err := config.Load(path); err == nil {
		t.Fatal("accepted network-reachable daemon API")
	}
}

func TestLoadDisablesRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	data := `{"baseURL":"http://localhost:8080","oidcIssuer":"https://id.example.test","oidcClientID":"sync","storageRoot":"/data","databasePath":"/db/app.sqlite","syncthingURL":"http://127.0.0.1:8384","registrationEnabled":false,"admins":[{"issuer":"https://id.example.test","subject":"operator"}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("APP_SYNCTHING_API_KEY", "api-secret")
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.RegistrationEnabled || len(got.Admins) != 1 || got.Admins[0].Subject != "operator" {
		t.Fatalf("unexpected config: %+v", got)
	}
}
