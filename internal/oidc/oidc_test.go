package oidc_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"syncthing-server/internal/oidc"
)

func TestAuthorizationCodePKCE(t *testing.T) {
	issuer := newTestIssuer(t)
	provider, err := oidc.New(context.Background(), issuer.URL, "client", "secret", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := oidc.NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("invalid PKCE verifier length: %d", len(verifier))
	}

	authURL, err := url.Parse(provider.AuthURL("state-123", verifier, "nonce-123"))
	if err != nil {
		t.Fatal(err)
	}
	q := authURL.Query()
	challenge := sha256.Sum256([]byte(verifier))
	if authURL.Path != "/authorize" || q.Get("response_type") != "code" || q.Get("client_id") != "client" || q.Get("redirect_uri") != "http://localhost/callback" || q.Get("state") != "state-123" || q.Get("nonce") != "nonce-123" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) {
		t.Fatalf("authorization URL missing required parameters: %s", authURL)
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("missing openid scope: %s", authURL)
	}

	gotIssuer, sub, name, email, err := provider.Exchange(context.Background(), "valid", verifier, "nonce-123")
	if err != nil {
		t.Fatal(err)
	}
	if gotIssuer != issuer.URL || sub != "stable-subject" || name != "Jane Doe" || email != "jane@example.net" {
		t.Fatalf("unexpected identity: (%q, %q, %q, %q)", gotIssuer, sub, name, email)
	}
}

func TestExchangeRejectsInvalidIdentity(t *testing.T) {
	issuer := newTestIssuer(t)
	provider, err := oidc.New(context.Background(), issuer.URL, "client", "secret", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ code, nonce string }{
		{"wrong-nonce", "nonce-123"},
		{"missing-nonce", "nonce-123"},
		{"wrong-audience", "nonce-123"},
		{"wrong-issuer", "nonce-123"},
		{"expired", "nonce-123"},
		{"missing-sub", "nonce-123"},
		{"missing-id-token", "nonce-123"},
		{"wrong-signature", "nonce-123"},
		{"valid", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			if _, _, _, _, err := provider.Exchange(context.Background(), tc.code, "test-verifier", tc.nonce); err == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
}

func newTestIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(t, w, map[string]any{
				"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			writeJSON(t, w, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "local-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" || r.Form.Get("redirect_uri") != "http://localhost/callback" {
				t.Errorf("invalid token request: %v", r.Form)
			}
			clientID, secret, basic := r.BasicAuth()
			if basic {
				if clientID != "client" || secret != "secret" {
					t.Errorf("invalid basic authentication")
				}
			} else if r.Form.Get("client_id") != "client" || r.Form.Get("client_secret") != "secret" {
				t.Errorf("invalid token authentication: %v", r.Form)
			}
			if r.Form.Get("code") == "missing-id-token" {
				writeJSON(t, w, map[string]any{"access_token": "access", "token_type": "Bearer"})
				return
			}
			claims := map[string]any{"iss": server.URL, "sub": "stable-subject", "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "nonce": "nonce-123", "name": "Jane Doe", "email": "jane@example.net"}
			switch r.Form.Get("code") {
			case "wrong-nonce":
				claims["nonce"] = "different"
			case "missing-nonce":
				delete(claims, "nonce")
			case "wrong-audience":
				claims["aud"] = "another-client"
			case "wrong-issuer":
				claims["iss"] = "http://another-issuer"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			case "missing-sub":
				delete(claims, "sub")
			}
			signingKey := key
			if r.Form.Get("code") == "wrong-signature" {
				var err error
				signingKey, err = rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Error(err)
					http.Error(w, "signing error", http.StatusInternalServerError)
					return
				}
			}
			writeJSON(t, w, map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": signIDToken(t, signingKey, claims)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func signIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "local-key"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("%s.%s", base64.RawURLEncoding.EncodeToString(header), base64.RawURLEncoding.EncodeToString(payload))
	hash := sha256.Sum256([]byte(body))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(signature)
}
