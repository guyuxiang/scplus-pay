package command

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/guyuxiang/scplus-pay/bootstrap"
	"github.com/guyuxiang/scplus-pay/config"
	"github.com/guyuxiang/scplus-pay/install"
	"github.com/guyuxiang/scplus-pay/middleware"
	"github.com/guyuxiang/scplus-pay/route"
	"github.com/guyuxiang/scplus-pay/util/constant"
	luluHttp "github.com/guyuxiang/scplus-pay/util/http"
	"github.com/guyuxiang/scplus-pay/util/log"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var httpCmd = &cobra.Command{
	Use:   "http",
	Short: "http service",
	Long:  "http service commands",
	Run: func(cmd *cobra.Command, args []string) {
	},
}

func init() {
	httpCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start",
	Long:  "start http service",
	Run: func(cmd *cobra.Command, args []string) {
		// If no config file exists, or if install=true is set in the config,
		// run the first-run install API on the same port as the main server.
		// The wizard writes the .env (with install=false) and shuts itself
		// down so bootstrap.InitApp() can read it normally on the same port.
		if config.NeedsInstall() {
			envPath, _ := config.ResolveConfigPath()
			install.RunInstallServer(install.DefaultInstallAddr, envPath)
		}
		bootstrap.InitApp()
		printBanner()
		HttpServerStart()
	},
}

func resolveWwwRoot() string {
	wwwRoot := "./www"
	if exePath, err := os.Executable(); err == nil {
		if exePath, err = filepath.EvalSymlinks(exePath); err == nil {
			wwwRoot = filepath.Join(filepath.Dir(exePath), "www")
		}
	}
	return wwwRoot
}

// spaIndexWithBasePath reads index.html and patches it for serving under a
// subpath: injects <base href="$basePath/"> and converts absolute asset paths
// to relative so the browser resolves them via the base href.
func spaIndexWithBasePath(wwwRoot, basePath string) echo.HandlerFunc {
	return func(c echo.Context) error {
		raw, err := os.ReadFile(filepath.Join(wwwRoot, "index.html"))
		if err != nil {
			return echo.ErrNotFound
		}
		html := strings.Replace(string(raw), "<head>",
			`<head><base href="`+basePath+`/">`, 1)
		for _, attr := range []string{"src", "href", "content"} {
			html = strings.ReplaceAll(html, attr+`="/`, attr+`="`)
		}
		return c.HTMLBlob(http.StatusOK, []byte(html))
	}
}

func HttpServerStart() {
	var err error
	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = customHTTPErrorHandler

	MiddlewareRegister(e)
	route.RegisterRoute(e)
	e.Static(config.StaticPath, config.StaticFilePath)

	// Resolve www/ relative to the executable so SPA routes work regardless
	// of the working directory. main.go extracts www/ next to the binary.
	wwwRoot := resolveWwwRoot()

	// If a base path is configured, serve the SPA and its assets under that
	// prefix so the app is reachable at e.g. /crypto-payment-gateway/admin.
	// API routes stay at their original paths because the SPA constructs API
	// URLs from window.location.origin (no path prefix).
	if bp := config.BasePath; bp != "" {
		indexHandler := spaIndexWithBasePath(wwwRoot, bp)
		bg := e.Group(bp)
		bg.GET("", indexHandler)
		bg.GET("/", indexHandler)
		bg.GET("/*", func(c echo.Context) error {
			sub := c.Param("*")
			fpath := filepath.Join(wwwRoot, filepath.FromSlash(sub))
			if info, statErr := os.Stat(fpath); statErr == nil && !info.IsDir() {
				return c.File(fpath)
			}
			return indexHandler(c)
		})
	}

	e.Use(echoMiddleware.StaticWithConfig(echoMiddleware.StaticConfig{
		Skipper: func(c echo.Context) bool {
			path := c.Request().URL.Path
			if path == "/install" || strings.HasPrefix(path, "/install/") {
				// The install wizard is only served by install.RunInstallServer
				// before bootstrap. Once main server starts, block /install.
				return true
			}
			if bp := config.BasePath; bp != "" && (path == bp || strings.HasPrefix(path, bp+"/")) {
				return true
			}
			return luluHttp.ShouldSkipSPAFallback(path)
		},
		HTML5: true,
		Index: "index.html",
		Root:  wwwRoot,
	}))

	httpListen := viper.GetString("http_listen")
	go func() {
		if err = e.Start(httpListen); err != http.ErrServerClosed {
			log.Sugar.Error(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, os.Kill)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}

func MiddlewareRegister(e *echo.Echo) {
	if config.HTTPAccessLog {
		e.Use(echoMiddleware.Logger())
	}
	e.Use(middleware.RequestUUID())
}

func customHTTPErrorHandler(err error, e echo.Context) {
	code := http.StatusInternalServerError
	msg := "server error"
	resp := &luluHttp.Response{
		StatusCode: code,
		Message:    msg,
		RequestID:  e.Request().Header.Get(echo.HeaderXRequestID),
	}
	// echo.HTTPError carries a real HTTP status (401 for auth failures,
	// 404 for missing routes, etc.). Propagate it instead of flattening
	// everything to 200 — clients rely on the status code.
	if he, ok := err.(*echo.HTTPError); ok {
		resp.StatusCode = he.Code
		if s, ok := he.Message.(string); ok {
			resp.Message = s
		} else if he.Message != nil {
			resp.Message = http.StatusText(he.Code)
		}
		_ = e.JSON(he.Code, resp)
		return
	}
	// Internal RspError: propagate Code as both the JSON status_code and
	// the real HTTP status when it maps to one (400/401/...); business
	// codes (>=1000) map to HTTP 400 so clients get a proper 4xx while
	// still reading the granular code from the body.
	if he, ok := err.(*constant.RspError); ok {
		resp.StatusCode = he.Code
		resp.Message = he.Msg
		_ = e.JSON(he.HttpStatus(), resp)
		return
	}
	_ = e.JSON(http.StatusInternalServerError, resp)
}
