// Package oidc implements provider-independent OIDC authorization-code login.
// Callers own state/nonce persistence and state validation at the callback.
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider discovers an OIDC issuer and verifies its issued ID tokens.
type Provider struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// New discovers the issuer's authorization, token, and signing-key endpoints.
func New(ctx context.Context, issuer, clientID, clientSecret, redirectURI string) (*Provider, error) {
	if issuer == "" || clientID == "" || redirectURI == "" {
		return nil, errors.New("oidc: issuer, client ID, and redirect URI are required")
	}
	discovered, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover issuer: %w", err)
	}
	return &Provider{
		config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Endpoint:     discovered.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: discovered.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

// NewVerifier generates an RFC 7636 PKCE verifier with 256 bits of entropy.
func NewVerifier() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("oidc: generate PKCE verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

// AuthURL returns a login redirect URL with S256 PKCE and an ID-token nonce.
// The caller must securely associate state, verifier, and nonce and check state on return.
func (p *Provider) AuthURL(state, verifier, nonce string) string {
	return p.config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce))
}

// Exchange redeems a code bound to verifier and validates the signed ID token
// (including issuer, audience, expiry, and nonce) before returning identity.
func (p *Provider) Exchange(ctx context.Context, code, verifier, nonce string) (issuer, subject, name, email string, err error) {
	token, err := p.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", "", "", "", fmt.Errorf("oidc: exchange authorization code: %w", err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return "", "", "", "", errors.New("oidc: token response has no ID token")
	}
	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return "", "", "", "", fmt.Errorf("oidc: verify ID token: %w", err)
	}
	if nonce == "" || idToken.Nonce != nonce {
		return "", "", "", "", errors.New("oidc: ID token nonce mismatch")
	}
	if idToken.Subject == "" {
		return "", "", "", "", errors.New("oidc: ID token has no subject")
	}
	var claims struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", "", "", fmt.Errorf("oidc: decode ID token claims: %w", err)
	}
	return idToken.Issuer, idToken.Subject, claims.Name, claims.Email, nil
}
