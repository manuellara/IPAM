package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient contains the discovered provider and OAuth client configuration.
type OIDCClient struct {
	provider    *oidc.Provider
	oauthConfig oauth2.Config
}

// OIDCClaims contains the identity fields IPAM uses after authentication.
type OIDCClaims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Nonce             string `json:"nonce"`
}

// NewOIDCClient discovers the provider and prepares an authorization client.
func NewOIDCClient(ctx context.Context, issuerURL, clientID, clientSecret, redirectURL string) (*OIDCClient, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, err
	}
	return &OIDCClient{
		provider: provider,
		oauthConfig: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
	}, nil
}

// AuthorizationURL returns the provider URL for the authorization-code flow.
func (c *OIDCClient) AuthorizationURL(state, nonce string) string {
	return c.oauthConfig.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce))
}

// VerifyCode exchanges an authorization code and validates its ID token.
func (c *OIDCClient) VerifyCode(ctx context.Context, code, nonce string) (OIDCClaims, error) {
	token, err := c.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return OIDCClaims{}, err
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return OIDCClaims{}, fmt.Errorf("OIDC provider did not return an identity token")
	}

	idToken, err := c.provider.Verifier(&oidc.Config{ClientID: c.oauthConfig.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return OIDCClaims{}, err
	}
	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		return OIDCClaims{}, err
	}
	if claims.Subject == "" || nonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(claims.Nonce)) != 1 {
		return OIDCClaims{}, fmt.Errorf("invalid OIDC identity claims")
	}
	return claims, nil
}

// RandomOIDCString returns a cryptographically random URL-safe value for OIDC state or nonce.
func RandomOIDCString() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
