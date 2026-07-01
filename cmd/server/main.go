package main

import (
	"fmt"
	"io"
	"os"

	"monica-proxy/internal/apiserver"
	"monica-proxy/internal/config"
	"monica-proxy/internal/logger"
	customMiddleware "monica-proxy/internal/middleware"
	"monica-proxy/internal/utils"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

type serverApp struct {
	config *config.Config
	server *echo.Echo
}

func newServerApp(cfg *config.Config) *serverApp {
	utils.InitHTTPClients(cfg)

	e := echo.New()
	e.Logger.SetOutput(io.Discard)
	e.HideBanner = true

	e.Server.ReadTimeout = cfg.Server.ReadTimeout
	e.Server.WriteTimeout = cfg.Server.WriteTimeout
	e.Server.IdleTimeout = cfg.Server.IdleTimeout

	e.Use(middleware.Recover())
	e.Use(middleware.CORS())
	e.Use(middleware.RequestID())
	e.Use(customMiddleware.RateLimit(cfg))

	apiserver.RegisterRoutes(e, cfg)

	return &serverApp{
		config: cfg,
		server: e,
	}
}

func (a *serverApp) start() error {
	return a.server.Start(a.config.GetAddress())
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logOutput := cfg.Logging.Output
	if logOutput == "file" {
		logOutput = "stdout"
	}
	logger.UpdateConfig(cfg.Logging.Level, cfg.Logging.Format, logOutput, cfg.Logging.MaskSensitive)

	app := newServerApp(cfg)
	logger.Info("starting monica proxy server", zap.String("address", cfg.GetAddress()))

	if err := app.start(); err != nil {
		logger.Fatal("server stopped", zap.Error(err))
	}
}
