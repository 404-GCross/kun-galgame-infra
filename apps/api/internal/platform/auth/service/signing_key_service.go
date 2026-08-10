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
	"api/pkg/oidctoken"

	"gorm.io/datatypes"
)

type SigningKeyService struct {
	repo *authrepo.SigningKeyRepository
	kek  []byte

	mu         sync.RWMutex
	pubCache   map[string]crypto.PublicKey
	lastReload time.Time
}

func NewSigningKeyService(repo *authrepo.SigningKeyRepository, kekSecret string) *SigningKeyService {
	return &SigningKeyService{repo: repo, kek: oidckeys.DeriveKEK(kekSecret)}
}

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

func (s *SigningKeyService) Key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	s.mu.RLock()
	k := s.pubCache[kid]
	s.mu.RUnlock()
	if k != nil {
		return k, nil
	}
	if err := s.reloadPublic(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", oidctoken.ErrKeyUnavailable, err)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pubCache != nil && time.Since(s.lastReload) < 30*time.Second {
		return nil
	}
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
	s.pubCache = m
	s.lastReload = time.Now()
	return nil
}

func (s *SigningKeyService) ActiveSigner(ctx context.Context, alg string) (kid string, key any, err error) {
	k, err := s.repo.FindActive(ctx, alg)
	if err != nil {
		return "", nil, err
	}
	der, err := oidckeys.Decrypt(s.kek, k.PrivateKeyEnc)
	if err != nil {
		return "", nil, err
	}
	priv, err := oidckeys.ParsePrivate(der)
	if err != nil {
		return "", nil, err
	}
	return k.Kid, priv, nil
}
