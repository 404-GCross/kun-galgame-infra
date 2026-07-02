// Package oidctoken is the token signing + verification core for the OIDC OP
// (Phase 1). It provides:
//
//   - Signer: mint access tokens with HS256 (legacy) or ES256 (asymmetric).
//   - Verifier: an "accept-both" verifier that validates ES256/RS256 tokens via
//     a kid->public-key Resolver AND, during the migration window, HS256 tokens
//     via the legacy shared secret. HMAC is verified only with the secret and
//     asymmetric only with public keys, so alg-confusion is structurally
//     impossible.
//   - Resolver: kid -> public key. JWKSResolver fetches + caches the OP's JWK
//     Set over HTTP (for the separate resource-server binaries).
//
// Design: docs/auth/03-oidc-standardization-design.md §5 / §10 Phase 1.
package oidctoken

import (
	"context"
	"crypto"
	"crypto/rand"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"api/pkg/oidckeys"
	"api/pkg/utils"

	"github.com/golang-jwt/jwt/v5"
)

// ErrKeyUnavailable marks a verification failure caused by the key store being
// unreachable (JWKS fetch / signing_keys query failed) — the service's problem,
// not the token's. Middlewares surface it as 503 so a logged-in caller retries
// instead of being treated as anonymous or logged out.
var ErrKeyUnavailable = stderrors.New("oidctoken: signing keys unavailable")

// Resolver resolves a JWS `kid` to the public key that verifies tokens signed
// with it.
type Resolver interface {
	Key(ctx context.Context, kid string) (crypto.PublicKey, error)
}

// --- Signers ------------------------------------------------------------

// Signer mints signed access tokens. Implementations differ only in algorithm.
type Signer interface {
	SignAccess(claims utils.TokenClaims, ttl time.Duration) (string, error)
}

// finalizeAccess stamps the registered claims (jti/exp/iat/nbf + RFC 9068
// iss/aud) identically on the HS256 and ES256 paths, so the ONLY difference
// between them is the signature — keeping the accept-both window purely about
// the signing algorithm. The audience (resource identifier, set by the caller
// per token) is preserved; everything else is minted here.
func finalizeAccess(claims utils.TokenClaims, ttl time.Duration, issuer string) (utils.TokenClaims, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return claims, err
	}
	now := time.Now()
	aud := claims.Audience
	claims.RegisteredClaims = jwt.RegisteredClaims{
		ID:        hex.EncodeToString(jti),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    issuer,
		Audience:  aud,
	}
	return claims, nil
}

// signAccess builds and signs the access token: RFC 9068 `typ: at+jwt` header
// (RFC 8725 token typing — keeps access tokens distinct from id_tokens), plus
// `kid` when set.
func signAccess(method jwt.SigningMethod, key any, kid, issuer string, claims utils.TokenClaims, ttl time.Duration) (string, error) {
	c, err := finalizeAccess(claims, ttl, issuer)
	if err != nil {
		return "", err
	}
	tok := jwt.NewWithClaims(method, c)
	tok.Header["typ"] = "at+jwt"
	if kid != "" {
		tok.Header["kid"] = kid
	}
	return tok.SignedString(key)
}

type hs256Signer struct {
	secret []byte
	issuer string
}

// NewHS256Signer signs with the legacy shared secret (HS256).
func NewHS256Signer(secret, issuer string) Signer {
	return &hs256Signer{secret: []byte(secret), issuer: issuer}
}

func (s *hs256Signer) SignAccess(claims utils.TokenClaims, ttl time.Duration) (string, error) {
	return signAccess(jwt.SigningMethodHS256, s.secret, "", s.issuer, claims, ttl)
}

type es256Signer struct {
	kid    string
	key    crypto.PrivateKey
	issuer string
}

// NewES256Signer signs with the active ES256 key, stamping its `kid` in the
// JWS header so verifiers can select the matching public key.
func NewES256Signer(kid string, key crypto.PrivateKey, issuer string) Signer {
	return &es256Signer{kid: kid, key: key, issuer: issuer}
}

func (s *es256Signer) SignAccess(claims utils.TokenClaims, ttl time.Duration) (string, error) {
	return signAccess(jwt.SigningMethodES256, s.key, s.kid, s.issuer, claims, ttl)
}

// --- ID token signer ----------------------------------------------------

// IDSigner mints OIDC id_tokens — always asymmetric (clients verify via JWKS),
// independent of the access-token HS256/ES256 flag. The algorithm comes from
// the signing key; the OP signs id_tokens RS256: it is the OIDC registration
// default for `id_token_signed_response_alg`, so standard RP libraries verify
// them with zero per-client alg configuration (and OIDC Core §15.1 makes RS256
// mandatory-to-implement anyway).
type IDSigner struct {
	kid    string
	method jwt.SigningMethod
	key    crypto.PrivateKey
	issuer string
}

// NewIDSigner builds an id_token signer from an active signing key (alg is the
// key's algorithm, e.g. "RS256") and the OP issuer identifier.
func NewIDSigner(kid, alg string, key crypto.PrivateKey, issuer string) *IDSigner {
	return &IDSigner{kid: kid, method: jwt.GetSigningMethod(alg), key: key, issuer: issuer}
}

// Sign issues a minimal id_token: iss/sub/aud/exp/iat (+ nonce when the auth
// request carried one). No roles/scope/authz data — the id_token authenticates
// the user to the client, nothing more.
func (s *IDSigner) Sign(sub, aud, nonce string, ttl time.Duration) (string, error) {
	if s.method == nil {
		return "", fmt.Errorf("oidctoken: unknown id_token signing alg")
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": s.issuer,
		"sub": sub,
		"aud": aud,
		"exp": now.Add(ttl).Unix(),
		"iat": now.Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	tok := jwt.NewWithClaims(s.method, claims)
	tok.Header["kid"] = s.kid
	return tok.SignedString(s.key)
}

// --- Verifier -----------------------------------------------------------

// Verifier validates access tokens. It accepts ES256/RS256 (via the Resolver)
// and, while acceptHS256 is set, HS256 (via the legacy secret) — the migration
// "accept-both" window. Drop the secret (empty) to make it asymmetric-only.
type Verifier struct {
	hs256Secret []byte   // nil => reject HMAC
	resolver    Resolver // nil => reject ES256/RS256
}

// NewVerifier builds an accept-both verifier. Pass hs256Secret="" to reject
// HMAC (post-migration) and resolver=nil to reject asymmetric (legacy-only).
func NewVerifier(hs256Secret string, resolver Resolver) *Verifier {
	var s []byte
	if hs256Secret != "" {
		s = []byte(hs256Secret)
	}
	return &Verifier{hs256Secret: s, resolver: resolver}
}

// NewVerifierWithJWKS is the resource-server convenience: an accept-both
// verifier whose asymmetric side fetches the OP's JWK Set from jwksURL. When
// jwksURL is empty the verifier is HS256-only (asymmetric disabled) — safe
// until asymmetric signing is turned on.
//
// The key cache is warmed in the background so a service restarting while the
// OP is briefly unreachable doesn't start with a cold cache that fails every
// ES256 token; a warm-up failure is non-fatal (the resolver refetches on
// demand) but logged loudly.
func NewVerifierWithJWKS(hs256Secret, jwksURL string) *Verifier {
	var r Resolver
	if jwksURL != "" {
		jr := NewJWKSResolver(jwksURL)
		r = jr
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := jr.refetch(ctx); err != nil {
				slog.Error("oidctoken: JWKS warm-up failed; ES256 verification degraded until the OP is reachable",
					"url", jwksURL, "err", err)
			}
		}()
	}
	return NewVerifier(hs256Secret, r)
}

// Parse validates the token and returns its claims. The keyfunc returns the
// HMAC secret ONLY for HMAC tokens and a public key ONLY for ES/RS tokens, so a
// token signed HS256 can never be validated against a public key (no
// alg-confusion), mirroring the legacy utils.ParseToken guard.
func (v *Verifier) Parse(ctx context.Context, tokenString string) (*utils.TokenClaims, error) {
	keyfunc := func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			if v.hs256Secret == nil {
				return nil, fmt.Errorf("oidctoken: HS256 tokens no longer accepted")
			}
			return v.hs256Secret, nil
		case *jwt.SigningMethodECDSA, *jwt.SigningMethodRSA:
			if v.resolver == nil {
				return nil, fmt.Errorf("oidctoken: asymmetric verification not configured")
			}
			kid, _ := t.Header["kid"].(string)
			if kid == "" {
				return nil, fmt.Errorf("oidctoken: token missing kid")
			}
			return v.resolver.Key(ctx, kid)
		default:
			return nil, fmt.Errorf("oidctoken: unexpected signing method: %v", t.Header["alg"])
		}
	}
	tok, err := jwt.ParseWithClaims(tokenString, &utils.TokenClaims{}, keyfunc)
	if err != nil {
		return nil, err
	}
	if claims, ok := tok.Claims.(*utils.TokenClaims); ok && tok.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}

// --- Remote JWKS resolver ----------------------------------------------

// JWKSResolver fetches + caches the OP's JWK Set over HTTP. Used by the separate
// resource-server binaries (galgame / image / artifact), which verify tokens
// with public keys only. On a cache miss (unknown kid) it refetches, throttled
// to minRefresh so a burst of unknown-kid tokens can't hammer the OP.
type JWKSResolver struct {
	url        string
	httpc      *http.Client
	minRefresh time.Duration

	mu        sync.RWMutex
	keys      map[string]crypto.PublicKey
	lastFetch time.Time
}

// NewJWKSResolver creates a resolver that fetches from the given JWKS URL (the
// internal OP address, e.g. http://oauth:9277/oauth/jwks).
func NewJWKSResolver(url string) *JWKSResolver {
	return &JWKSResolver{
		url:        url,
		httpc:      &http.Client{Timeout: 5 * time.Second},
		minRefresh: 30 * time.Second,
	}
}

// Key returns the public key for a kid, refetching the JWK Set on a miss.
func (r *JWKSResolver) Key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	r.mu.RLock()
	k := r.keys[kid]
	r.mu.RUnlock()
	if k != nil {
		return k, nil
	}
	if err := r.refetch(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnavailable, err)
	}
	r.mu.RLock()
	k = r.keys[kid]
	r.mu.RUnlock()
	if k == nil {
		return nil, fmt.Errorf("oidctoken: unknown kid %q", kid)
	}
	return k, nil
}

func (r *JWKSResolver) refetch(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Another goroutine may have just refreshed while we waited for the lock.
	if r.keys != nil && time.Since(r.lastFetch) < r.minRefresh {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	resp, err := r.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidctoken: jwks fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	keys, err := oidckeys.ParseJWKSet(body)
	if err != nil {
		return err
	}
	r.keys = keys
	r.lastFetch = time.Now()
	return nil
}
