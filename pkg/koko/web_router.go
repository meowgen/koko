package koko

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"

	"github.com/gin-gonic/gin"
	"github.com/meowgen/koko/pkg/auth"
	"github.com/meowgen/koko/pkg/common"
	"github.com/meowgen/koko/pkg/config"
	"github.com/meowgen/koko/pkg/httpd"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
)

func registerWebHandlers(jmsService *service.JMService, webSrv *httpd.Server) {
	if config.GlobalConfig.LogLevel != "DEBUG" {
		gin.SetMode(gin.ReleaseMode)
	}
	eng := gin.New()
	trustedProxies := []string{"0.0.0.0/0", "::/0"}
	if err := eng.SetTrustedProxies(trustedProxies); err != nil {
		log.Fatal(err)
	}
	eng.Use(gin.Recovery())
	eng.Use(gin.Logger())
	rootGroup := eng.Group("")
	kokoGroup := rootGroup.Group("/koko")
	kokoGroup.Static("/static/", "./static")
	kokoGroup.Static("/assets", "./ui/dist/assets")
	kokoGroup.StaticFile("/favicon.ico", "./ui/dist/favicon.ico")
	kokoGroup.GET("/health/", webSrv.HealthStatusHandler)
	eng.LoadHTMLFiles("/opt/koko/templates/elfinder/file_manager.html")
	wsGroup := kokoGroup.Group("/ws/")
	{
		wsGroup.Group("/terminal").Use(
			auth.HTTPMiddleSessionAuth(jmsService)).GET("/", webSrv.ProcessTerminalWebsocket)

		wsGroup.Group("/elfinder").Use(
			auth.HTTPMiddleSessionAuth(jmsService)).GET("/", webSrv.ProcessElfinderWebsocket)

		wsGroup.Group("/token").GET("/", webSrv.ProcessTokenWebsocket)
	}

	terminalGroup := kokoGroup.Group("/terminal")
	terminalGroup.Use(auth.HTTPMiddleSessionAuth(jmsService))
	{
		terminalGroup.GET("/", func(ctx *gin.Context) {
			ctx.File("./ui/dist/index.html")
		})
	}
	shareGroup := kokoGroup.Group("/share")
	shareGroup.Use(auth.HTTPMiddleSessionAuth(jmsService))
	{
		shareGroup.GET("/:id/", func(ctx *gin.Context) {
			ctx.File("./ui/dist/index.html")
		})
	}

	monitorGroup := kokoGroup.Group("/monitor")
	monitorGroup.Use(auth.HTTPMiddleSessionAuth(jmsService))
	{
		monitorGroup.GET("/:id/", func(ctx *gin.Context) {
			ctx.File("./ui/dist/index.html")
		})
	}

	tokenGroup := kokoGroup.Group("/token")
	{
		tokenGroup.GET("/", func(ctx *gin.Context) {
			ctx.File("./ui/dist/index.html")
		})

		tokenGroup.GET("/:id/", func(ctx *gin.Context) {
			ctx.File("./ui/dist/index.html")
		})
	}
	elfindlerGroup := kokoGroup.Group("/elfinder")
	elfindlerGroup.Use(auth.HTTPMiddleSessionAuth(jmsService))
	{
		elfindlerGroup.GET("/sftp/", func(ctx *gin.Context) {
			metaData := webSrv.GenerateViewMeta("_")
			ctx.HTML(http.StatusOK, "file_manager.html", metaData)
		})
		elfindlerGroup.GET("/sftp/:host/", func(ctx *gin.Context) {
			hostId := ctx.Param("host")
			if ok := common.ValidUUIDString(hostId); !ok {
				ctx.AbortWithStatus(http.StatusBadRequest)
				return
			}
			metaData := webSrv.GenerateViewMeta(hostId)
			ctx.HTML(http.StatusOK, "file_manager.html", metaData)
		})
		elfindlerGroup.Any("/connector/:host/", webSrv.SftpHostConnectorView)
	}

	debugGroup := rootGroup.Group("/debug/pprof")
	debugGroup.Use(auth.HTTPMiddleDebugAuth())
	{
		debugGroup.GET("/", gin.WrapF(pprof.Index))
		debugGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		debugGroup.GET("/profile", gin.WrapF(pprof.Profile))
		debugGroup.POST("/symbol", gin.WrapF(pprof.Symbol))
		debugGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
		debugGroup.GET("/trace", gin.WrapF(pprof.Trace))
		debugGroup.GET("/allocs", gin.WrapF(pprof.Handler("allocs").ServeHTTP))
		debugGroup.GET("/block", gin.WrapF(pprof.Handler("block").ServeHTTP))
		debugGroup.GET("/goroutine", gin.WrapF(pprof.Handler("goroutine").ServeHTTP))
		debugGroup.GET("/heap", gin.WrapF(pprof.Handler("heap").ServeHTTP))
		debugGroup.GET("/mutex", gin.WrapF(pprof.Handler("mutex").ServeHTTP))
		debugGroup.GET("/threadcreate", gin.WrapF(pprof.Handler("threadcreate").ServeHTTP))
	}
	conf := config.GetConf()
	addr := net.JoinHostPort(conf.BindHost, conf.HTTPPort)
	webSrv.Srv = &http.Server{
		Addr:    addr,
		Handler: eng,
	}
}
