package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	listenAddress = ":8082"
	issuer        = "http://mock-oidc:8082"
	clientID      = "team-memory-e2e"
	keyID         = "team-memory-e2e-key"
)

type provider struct {
	key    *rsa.PrivateKey
	nonces sync.Map
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := checkHealth(&http.Client{Timeout: time.Second}, "http://127.0.0.1:8082/healthz"); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	service, err := newProvider()
	if err != nil {
		log.Fatalf("initialize deterministic OIDC provider: %v", err)
	}
	server := newServer(service)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("serve deterministic OIDC provider: %v", err)
	}
}

func newServer(service *provider) *http.Server {
	return &http.Server{
		Addr: listenAddress, Handler: newHandler(service),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
	}
}

func newProvider() (*provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate OIDC signing key: %w", err)
	}
	return &provider{key: key}, nil
}

func newHandler(service *provider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /.well-known/openid-configuration", service.discovery)
	mux.HandleFunc("GET /authorize", service.authorize)
	mux.HandleFunc("POST /token", service.token)
	mux.HandleFunc("GET /keys", service.keys)
	return mux
}

func (p *provider) health(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusOK)
}

func (p *provider) discovery(response http.ResponseWriter, _ *http.Request) {
	p.writeJSON(response, map[string]any{
		"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
		"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
		"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *provider) authorize(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	redirectURL, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || redirectURL.Scheme == "" || query.Get("state") == "" || query.Get("nonce") == "" {
		http.Error(response, "invalid authorization request", http.StatusBadRequest)
		return
	}
	code := query.Get("state")
	p.nonces.Store(code, query.Get("nonce"))
	callback := redirectURL.Query()
	callback.Set("code", code)
	callback.Set("state", query.Get("state"))
	redirectURL.RawQuery = callback.Encode()
	http.Redirect(response, request, redirectURL.String(), http.StatusFound)
}

func (p *provider) token(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid token request", http.StatusBadRequest)
		return
	}
	code := request.Form.Get("code")
	nonceValue, ok := p.nonces.LoadAndDelete(code)
	if !ok {
		http.Error(response, "invalid authorization code", http.StatusBadRequest)
		return
	}
	nonce, ok := nonceValue.(string)
	if !ok {
		http.Error(response, "invalid nonce", http.StatusBadRequest)
		return
	}
	idToken, err := p.signIDToken(nonce)
	if err != nil {
		http.Error(response, "signing failed", http.StatusInternalServerError)
		return
	}
	p.writeJSON(response, map[string]any{
		"access_token": "e2e-access-token", "token_type": "Bearer", "expires_in": 300,
		"id_token": idToken,
	})
}

func (p *provider) keys(response http.ResponseWriter, _ *http.Request) {
	p.writeJSON(response, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &p.key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig",
	}}})
}

func (p *provider) signIDToken(nonce string) (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		return "", fmt.Errorf("create OIDC signer: %w", err)
	}
	now := time.Now().UTC()
	token, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer: issuer, Subject: "e2e-owner", Audience: jwt.Audience{clientID},
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}).Claims(struct {
		Nonce         string `json:"nonce"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}{Nonce: nonce, Email: "owner@example.com", EmailVerified: true, Name: "E2E Owner"}).Serialize()
	if err != nil {
		return "", fmt.Errorf("serialize OIDC ID token: %w", err)
	}
	return token, nil
}

func (p *provider) writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		log.Printf("encode OIDC response: %v", err)
	}
}

func checkHealth(client *http.Client, endpoint string) error {
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("check deterministic OIDC health: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return fmt.Errorf("close deterministic OIDC health response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("check deterministic OIDC health: status %d", response.StatusCode)
	}
	return nil
}
