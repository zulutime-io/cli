package devicekey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type KeyPair struct {
	PublicKeyB64  string
	PrivateKeyB64 string
	Alg           string
}

func Generate() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		PublicKeyB64:  base64.StdEncoding.EncodeToString(pub),
		PrivateKeyB64: base64.StdEncoding.EncodeToString(priv),
		Alg:           "Ed25519",
	}, nil
}

func Canonical(timestamp, method, path string, body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("v1\n%s\n%s\n%s\n%s", timestamp, strings.ToUpper(method), path, hex.EncodeToString(sum[:]))
}

func Sign(privateKeyB64, method, path string, body []byte) (timestamp, signature string, err error) {
	raw, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return "", "", fmt.Errorf("invalid device private key")
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	msg := []byte(Canonical(ts, method, path, body))
	sig := ed25519.Sign(ed25519.PrivateKey(raw), msg)
	return ts, base64.StdEncoding.EncodeToString(sig), nil
}
