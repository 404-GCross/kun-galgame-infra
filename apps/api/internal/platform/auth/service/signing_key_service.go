package service

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"api/internal/platform/auth/model"
	authrepo "api/internal/platform/auth/repository"
	"api/pkg/oidckeys"

	"gorm.io/datatypes"
)

// SigningKeyService owns the OIDC signing-key lifecycle: bootstrap, JWKS
// assembly, and access to the active signer. Private keys are decrypted only
// in-process, never exposed.
//
// Design: docs/auth/03-oidc-standardization-design.md §4.
type SigningKeyService struct {
	repo *authrepo.SigningKeyRepository
	kek  []byte

	// pubCache is the parsed published public keys (kid -> key), the local
	// implementation of oidctoken.Resolver for the OP's own verification.
	mu       sync.RWMutex
	pubCache map[string]crypto.PublicKey
}

// NewSigningKeyService creates the service. kekSecret is KUN_OIDC_KEY_ENC_KEY;
// it is stretched to a 32-byte AES key. Callers must ensure kekSecret is
// non-empty (cmd/oauth gates OIDC key infra on it).
func NewSigningKeyService(repo *authrepo.SigningKeyRepository, kekSecret string) *SigningKeyService {
	return &SigningKeyService{repo: repo, kek: oidckeys.DeriveKEK(kekSecret)}
}

// EnsureBootstrapped generates an initial active key for each supported alg if
// none exists. Idempotent — safe to call on every startup. ES256 is the active
// signer; RS256 is published too so verifiers that require RS256 (OIDC Core
// §15.1 mandatory-to-implement) can validate.
func (s *SigningKeyService) EnsureBootstrapped(ctx context.Context) error {
	for _, alg := range []string{oidckeys.AlgES256, oidckeys.AlgRS256} {
		n, err := s.repo.CountActive(ctx, alg)
		if err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if err := s.generate(ctx, alg, "active"); err != nil {
			return fmt.Errorf("bootstrap %s: %w", alg, err)
		}
		slog.Info("oidc signing key bootstrapped", "alg", alg)
	}
	return nil
}

// generate creates, encrypts, and stores a new key in the given state.
func (s *SigningKeyService) generate(ctx context.Context, alg, state string) error {
	km, err := oidckeys.Generate(alg)
	if err != nil {
		return err
	}
	enc, err := oidckeys.Encrypt(s.kek, km.PrivateDER)
	if err != nil {
		return err
	}
	pubJSON, err := json.Marshal(km.PublicJWK)
	if err != nil {
		return err
	}
	k := &model.SigningKey{
		Kid:           km.Kid,
		Alg:           km.Alg,
		Use:           "sig",
		PublicJWK:     datatypes.JSON(pubJSON),
		PrivateKeyEnc: enc,
		State:         state,
	}
	if state == "active" {
		now := time.Now()
		k.ActivatedAt = &now
	}
	return s.repo.Create(ctx, k)
}

// JWKS returns the public JWK Set: {"keys":[...]} of all published keys.
func (s *SigningKeyService) JWKS(ctx context.Context) (map[string]any, error) {
	keys, err := s.repo.FindPublished(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(keys))
	for i := range keys {
		out = append(out, json.RawMessage(keys[i].PublicJWK))
	}
	return map[string]any{"keys": out}, nil
}

// Key implements oidctoken.Resolver: it maps a JWS kid to the published public
// key, reloading the cache on a miss (e.g. after a rotation adds a new key).
func (s *SigningKeyService) Key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	s.mu.RLock()
	k := s.pubCache[kid]
	s.mu.RUnlock()
	if k != nil {
		return k, nil
	}
	if err := s.reloadPublic(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	k = s.pubCache[kid]
	s.mu.RUnlock()
	if k == nil {
		return nil, fmt.Errorf("signing key: unknown kid %q", kid)
	}
	return k, nil
}

func (s *SigningKeyService) reloadPublic(ctx context.Context) error {
	keys, err := s.repo.FindPublished(ctx)
	if err != nil {
		return err
	}
	m := make(map[string]crypto.PublicKey, len(keys))
	for i := range keys {
		var jwk map[string]any
		if err := json.Unmarshal(keys[i].PublicJWK, &jwk); err != nil {
			continue
		}
		pub, err := oidckeys.PublicKeyFromJWK(jwk)
		if err != nil {
			continue
		}
		m[keys[i].Kid] = pub
	}
	s.mu.Lock()
	s.pubCache = m
	s.mu.Unlock()
	return nil
}

// ActiveSigner returns the parsed private key + kid + alg for the ES256 active
// signer (the token signer, used from Phase 1). Exposed in Phase 0 so the
// decrypt/parse path is wired and testable end-to-end.
func (s *SigningKeyService) ActiveSigner(ctx context.Context) (kid, alg string, key any, err error) {
	k, err := s.repo.FindActive(ctx, oidckeys.AlgES256)
	if err != nil {
		return "", "", nil, err
	}
	der, err := oidckeys.Decrypt(s.kek, k.PrivateKeyEnc)
	if err != nil {
		return "", "", nil, err
	}
	priv, err := oidckeys.ParsePrivate(der)
	if err != nil {
		return "", "", nil, err
	}
	return k.Kid, k.Alg, priv, nil
}
