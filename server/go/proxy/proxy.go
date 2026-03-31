package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimits holds per-connection bandwidth caps in bytes/second.
// A zero value means unlimited.
type RateLimits struct {
	UpBytesPerSec   int64 // client → server
	DownBytesPerSec int64 // server → client
}

// Resolver maps a client IP to its RateLimits.
type Resolver interface {
	Resolve(clientIP string) RateLimits
}

// Server is a TCP reverse proxy with optional per-connection bandwidth limiting.
type Server struct {
	ListenAddr  string   // e.g., ":8443"
	BackendAddr string   // e.g., "127.0.0.1:18443"
	Resolver    Resolver // nil = no rate limiting
}

// Run accepts connections until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	slog.Info("proxy: listening", "addr", s.ListenAddr, "backend", s.BackendAddr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Warn("proxy: accept error", "err", err)
				continue
			}
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, clientConn net.Conn) {
	defer clientConn.Close()

	// Resolve rate limits by client IP.
	var limits RateLimits
	if s.Resolver != nil {
		host, _, _ := net.SplitHostPort(clientConn.RemoteAddr().String())
		limits = s.Resolver.Resolve(host)
	}

	backendConn, err := net.DialTimeout("tcp", s.BackendAddr, 5*time.Second)
	if err != nil {
		slog.Debug("proxy: backend dial failed", "err", err)
		return
	}
	defer backendConn.Close()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// client → backend (client upload)
	go func() {
		defer wg.Done()
		defer cancel()
		var w io.Writer = backendConn
		if limits.UpBytesPerSec > 0 {
			lim := rate.NewLimiter(rate.Limit(limits.UpBytesPerSec), int(limits.UpBytesPerSec))
			w = &rateLimitedWriter{w: backendConn, lim: lim, ctx: connCtx}
		}
		io.Copy(w, clientConn)
		if tc, ok := backendConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// backend → client (client download)
	go func() {
		defer wg.Done()
		defer cancel()
		var w io.Writer = clientConn
		if limits.DownBytesPerSec > 0 {
			lim := rate.NewLimiter(rate.Limit(limits.DownBytesPerSec), int(limits.DownBytesPerSec))
			w = &rateLimitedWriter{w: clientConn, lim: lim, ctx: connCtx}
		}
		io.Copy(w, backendConn)
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}

// rateLimitedWriter wraps an io.Writer with a token-bucket bandwidth limiter.
// Writes are chunked to ≤4096 bytes for smooth rate limiting.
type rateLimitedWriter struct {
	w   io.Writer
	lim *rate.Limiter
	ctx context.Context
}

func (rlw *rateLimitedWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > 4096 {
			n = 4096
		}
		if err := rlw.lim.WaitN(rlw.ctx, n); err != nil {
			return total, err
		}
		written, err := rlw.w.Write(p[:n])
		total += written
		if err != nil {
			return total, err
		}
		p = p[written:]
	}
	return total, nil
}
