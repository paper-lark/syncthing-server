// A loopback-only OIDC issuer for manual wrapper verification. Never use in production.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	issuer := "http://127.0.0.1:48555"
	var nonce, challenge string
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(value)
	}
	http.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	http.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "smoke", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	http.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		nonce, challenge = r.URL.Query().Get("nonce"), r.URL.Query().Get("code_challenge")
		target, err := url.Parse(r.URL.Query().Get("redirect_uri"))
		if err != nil {
			http.Error(w, "invalid redirect", 400)
			return
		}
		query := target.Query()
		query.Set("code", "smoke-code")
		query.Set("state", r.URL.Query().Get("state"))
		target.RawQuery = query.Encode()
		http.Redirect(w, r, target.String(), http.StatusSeeOther)
	})
	http.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			http.Error(w, "PKCE mismatch", 400)
			return
		}
		claims := map[string]any{"iss": issuer, "sub": "smoke-user", "aud": "smoke-client", "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "name": "Smoke user"}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "smoke"})
		payload, _ := json.Marshal(claims)
		input := strings.Join([]string{base64.RawURLEncoding.EncodeToString(header), base64.RawURLEncoding.EncodeToString(payload)}, ".")
		hash := sha256.Sum256([]byte(input))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		if err != nil {
			http.Error(w, "token signing failed", 500)
			return
		}
		write(w, map[string]string{"access_token": "test-only", "token_type": "Bearer", "id_token": fmt.Sprintf("%s.%s", input, base64.RawURLEncoding.EncodeToString(signature))})
	})
	log.Printf("test-only OIDC issuer: %s", issuer)
	log.Fatal(http.ListenAndServe("127.0.0.1:48555", nil))
}
