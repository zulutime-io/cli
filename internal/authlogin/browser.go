package authlogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type PKCE struct {
	Verifier  string
	Challenge string
	State     string
	UserCode  string
}

func NewPKCE() (*PKCE, error) {
	verBytes := make([]byte, 32)
	if _, err := rand.Read(verBytes); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, err
	}
	userCode, err := NewUserCode()
	if err != nil {
		return nil, err
	}
	return &PKCE{
		Verifier:  verifier,
		Challenge: challenge,
		State:     hex.EncodeToString(stateBytes),
		UserCode:  userCode,
	}, nil
}

func NewUserCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func UserCodeChallenge(userCode string) string {
	sum := sha256.Sum256([]byte(userCode))
	return hex.EncodeToString(sum[:])
}

type CallbackResult struct {
	Code  string
	State string
	Error string
}

type CallbackServer struct {
	RedirectURI string
	resultCh    chan CallbackResult
	srv         *http.Server
	ln          net.Listener
}

func callbackPage(title, heading, body string, ok bool) string {
	accent := "#0d9488"
	mark := "✓"
	if !ok {
		accent = "#64748b"
		mark = "–"
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>%s</title>
  <style>
    :root { color-scheme: light dark; }
    * { box-sizing: border-box; }
    body {
      margin: 0; min-height: 100dvh; display: grid; place-items: center;
      font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, sans-serif;
      background: radial-gradient(1200px 600px at 20%% -10%%, rgba(13,148,136,.18), transparent 55%%),
                  radial-gradient(900px 500px at 100%% 0%%, rgba(15,23,42,.06), transparent 50%%),
                  #f8fafc;
      color: #0f172a;
      padding: 1.5rem;
    }
    @media (prefers-color-scheme: dark) {
      body {
        background: radial-gradient(1200px 600px at 20%% -10%%, rgba(45,212,191,.12), transparent 55%%),
                    #0b1220;
        color: #e2e8f0;
      }
      .card { background: rgba(15,23,42,.85); border-color: rgba(148,163,184,.2); }
      .muted { color: #94a3b8; }
    }
    .card {
      width: min(28rem, 100%%);
      background: rgba(255,255,255,.92);
      border: 1px solid rgba(15,23,42,.08);
      border-radius: 1rem;
      padding: 2rem 1.75rem;
      box-shadow: 0 20px 50px rgba(15,23,42,.08);
      text-align: center;
    }
    .brand { font-weight: 700; letter-spacing: .02em; color: %s; margin: 0 0 .75rem; font-size: .95rem; }
    h1 { margin: 0; font-size: 1.5rem; line-height: 1.25; font-weight: 650; }
    .muted { margin: .85rem 0 0; color: #64748b; font-size: .95rem; line-height: 1.5; }
    .mark {
      width: 3rem; height: 3rem; margin: 0 auto 1.1rem; border-radius: 999px;
      display: grid; place-items: center; background: color-mix(in srgb, %s 16%%, transparent);
      color: %s; font-size: 1.35rem; font-weight: 700;
    }
  </style>
</head>
<body>
  <main class="card">
    <div class="mark">%s</div>
    <p class="brand">ZuluTime</p>
    <h1>%s</h1>
    <p class="muted">%s</p>
  </main>
</body>
</html>`, title, accent, accent, accent, mark, heading, body)
}

func StartCallbackServer(expectedState string) (*CallbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cs := &CallbackServer{
		RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
		resultCh:    make(chan CallbackResult, 1),
		ln:          ln,
		srv:         &http.Server{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := CallbackResult{
			Code:  q.Get("code"),
			State: q.Get("state"),
			Error: q.Get("error"),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.Error != "" {
			_, _ = w.Write([]byte(callbackPage(
				"ZuluTime CLI",
				"Access denied",
				"You can close this window and return to the terminal.",
				false,
			)))
			select {
			case cs.resultCh <- res:
			default:
			}
			return
		}
		if res.State != expectedState || res.Code == "" {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			select {
			case cs.resultCh <- CallbackResult{Error: "invalid_callback", State: res.State}:
			default:
			}
			return
		}
		_, _ = w.Write([]byte(callbackPage(
			"ZuluTime CLI — signed in",
			"You're signed in",
			"You can close this window and return to the terminal.",
			true,
		)))
		select {
		case cs.resultCh <- res:
		default:
		}
	})
	cs.srv.Handler = mux
	go func() { _ = cs.srv.Serve(ln) }()
	return cs, nil
}

func (cs *CallbackServer) Wait(ctx context.Context) (CallbackResult, error) {
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cs.srv.Shutdown(shutdownCtx)
	}()
	select {
	case <-ctx.Done():
		return CallbackResult{}, ctx.Err()
	case res := <-cs.resultCh:
		if res.Error != "" {
			if res.Error == "access_denied" {
				return res, errors.New("access denied in browser")
			}
			return res, fmt.Errorf("authorize failed: %s", res.Error)
		}
		return res, nil
	}
}

func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("unsupported OS for opening browser: %s", runtime.GOOS)
	}
	return cmd.Start()
}

func AuthorizeURL(webOrigin, loginID string) string {
	base := strings.TrimRight(webOrigin, "/")
	q := url.Values{}
	q.Set("login_id", loginID)
	return base + "/cli/authorize?" + q.Encode()
}

func DefaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "CLI"
	}
	return "CLI (" + host + ")"
}
