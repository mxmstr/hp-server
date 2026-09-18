package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/local/reorigin-hotpursuit/hpserver"
	"github.com/local/reorigin-hotpursuit/legacytls"
)

func main() {

	redirectorAddr := flag.String("redirector", "0.0.0.0:42127", "ProtoSSL redirector listener")
	blazeAddr := flag.String("blaze", "0.0.0.0:10013", "ProtoSSL Blaze listener")
	autologAddr := flag.String("autolog", "0.0.0.0:18080", "Autolog HTTP listener")
	autologURL := flag.String("autolog-url", "http://autolog1.ea.com:18080", "Autolog base URL advertised through Blaze")
	certFile := flag.String("cert", "", "PEM certificate chain (optional; must be used with -key)")
	keyFile := flag.String("key", "", "PEM RSA private key (optional; must be used with -cert)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	var tlsConfig *legacytls.Config
	var err error

	if *certFile != "" || *keyFile != "" {
		if *certFile == "" || *keyFile == "" {
			logger.Error("both -cert and -key are required")
			os.Exit(2)
		}
		tlsConfig, err = legacytls.LoadKeyPair(*certFile, *keyFile)
	} else {
		tlsConfig, err = legacytls.GenerateSelfSigned("gosredirector.ea.com", "gosredirector.online.ea.com", "nfshp-prd2-mp-app-01.ea.com", "autolog1.ea.com", "localhost")
	}

	if err != nil {
		logger.Error("certificate setup failed", "error", err)
		os.Exit(1)
	}

	tlsConfig.Trace = func(event string, attrs ...any) {
		logger.Info("ProtoSSL "+event, attrs...)
	}

	redirectorTCP, err := net.Listen("tcp", *redirectorAddr)
	if err != nil {
		logger.Error("redirector listen failed", "error", err)
		os.Exit(1)
	}
	defer redirectorTCP.Close()

	blazeTCP, err := net.Listen("tcp", *blazeAddr)
	if err != nil {
		logger.Error("blaze listen failed", "error", err)
		os.Exit(1)
	}
	defer blazeTCP.Close()

	autologTCP, err := net.Listen("tcp", *autologAddr)
	if err != nil {
		logger.Error("Autolog listen failed", "error", err)
		os.Exit(1)
	}
	defer autologTCP.Close()

	redirector := legacytls.NewListener(redirectorTCP, tlsConfig)
	blaze := legacytls.NewListener(blazeTCP, tlsConfig)

	logger.Info("HP2010 legacy FIRE server", "redirector", redirector.Addr(), "blaze", blaze.Addr(), "autolog", autologTCP.Addr(), "transport", "TLS 1.0 RSA/RC4")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		redirector.Close()
		blaze.Close()
		autologTCP.Close()
	}()

	errCh := make(chan error, 3)
	server := hpserver.New(logger)
	server.AutologBase = *autologURL

	go func() { errCh <- server.Serve(ctx, redirector) }()
	go func() { errCh <- server.Serve(ctx, blaze) }()
	go func() {
		httpServer := &http.Server{
			Handler: hpserver.AutologHandler(logger),
			ConnState: func(conn net.Conn, state http.ConnState) {
				logger.Info("Autolog connection", "remote", conn.RemoteAddr(), "state", state.String())
			},
		}
		errCh <- httpServer.Serve(autologTCP)
	}()

	if err := <-errCh; err != nil && ctx.Err() == nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}

}
