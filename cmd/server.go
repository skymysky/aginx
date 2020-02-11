package cmd

import (
	"fmt"
	"github.com/ihaiker/aginx/lego"
	"github.com/ihaiker/aginx/nginx/client"
	"github.com/ihaiker/aginx/nginx/configuration"
	"github.com/ihaiker/aginx/server"
	"github.com/ihaiker/aginx/storage"
	"github.com/ihaiker/aginx/storage/consul"
	fileStorage "github.com/ihaiker/aginx/storage/file"
	. "github.com/ihaiker/aginx/util"
	"github.com/spf13/cobra"
	"net/url"
	"os"
	"strings"
)

func getString(cmd *cobra.Command, key string) string {
	envKey := strings.ToUpper(fmt.Sprintf("aginx_%s", key))
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	value, err := cmd.PersistentFlags().GetString(key)
	PanicIfError(err)
	return value
}

func clusterConfiguration(cluster string) (engine storage.Engine) {
	var err error
	if cluster == "" {
		engine, err = fileStorage.System()
		PanicIfError(err)
	} else {
		config, err := url.Parse(cluster)
		PanicIfError(err)

		switch config.Scheme {
		case "consul":
			token := config.Query().Get("token")
			folder := config.EscapedPath()[1:]
			engine, err = consul.New(config.Host, folder, token)
			PanicIfError(err)
		}
	}
	return
}

func selectDirective(api *client.Client, domain string) (queries []string, directive *configuration.Directive) {
	serverQuery := fmt.Sprintf("server.[server_name('%s') & listen('80')]", domain)
	queries = client.Queries("http", "include", "*", serverQuery)
	if directives, err := api.Select(queries...); err == nil {
		directive = directives[0]
		return
	}
	queries = client.Queries("http", serverQuery)
	if directives, err := api.Select(queries...); err == nil {
		directive = directives[0]
		return
	}
	return
}

func apiServer(domain, address string) *configuration.Directive {
	directive := configuration.NewDirective("server")
	directive.AddBody("listen", "80")
	directive.AddBody("server_name", domain)

	if strings.HasPrefix(address, ":") {
		address = "127.0.0.1" + address
	}
	location := directive.AddBody("location", "/")
	location.AddBody("proxy_redirect", "default")
	location.AddBody("proxy_pass", fmt.Sprintf("http://%s", address))
	location.AddBody("proxy_set_header", "Host", "$host")
	location.AddBody("proxy_set_header", "X-Real-IP", "$remote_addr")
	location.AddBody("proxy_set_header", "X-Forwarded-For", "$proxy_add_x_forwarded_for")
	location.AddBody("client_max_body_size", "10m")
	location.AddBody("client_body_buffer_size", "128k")
	location.AddBody("proxy_connect_timeout", "90")
	location.AddBody("proxy_send_timeout", "90")
	location.AddBody("proxy_read_timeout", "90")
	location.AddBody("proxy_buffers", "32", "4k")
	return directive
}

func exposeApi(cmd *cobra.Command, engine storage.Engine) {
	address := getString(cmd, "api")
	domain := getString(cmd, "expose")
	if domain == "" {
		return
	}
	api, err := client.NewClient(engine)
	PanicIfError(err)

	_, directive := selectDirective(api, domain)
	if directive == nil {
		apiServer := apiServer(domain, address)

		err = api.Add(client.Queries("http"), apiServer)
		PanicIfError(err)

		err = engine.StoreConfiguration(api.Configuration())
		PanicIfError(err)
	}
}

var ServerCmd = &cobra.Command{
	Use: "server", Long: "the api server",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer Catch(func(err error) {
			fmt.Println(err)
		})

		address := getString(cmd, "api")
		auth := getString(cmd, "security")

		daemon := NewDaemon()
		cluster := getString(cmd, "cluster")
		engine := clusterConfiguration(cluster)
		if service, matched := engine.(Service); matched {
			daemon.Add(service)
		}

		exposeApi(cmd, engine)

		manager, err := lego.NewManager(engine)
		PanicIfError(err)

		svr := new(server.Supervister)
		routers := server.Routers(svr, engine, manager, auth)
		http := server.NewHttp(address, routers)

		return daemon.Add(http, svr, manager).Start()
	},
}

func AddClusterFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringP("cluster", "c", "", `cluster config
for example. 
	consul://127.0.0.1:8500/aginx?token=authtoken   config from consul.  
	zk://127.0.0.1:2182/aginx                       config from zookeeper.
	etcd://127.0.0.1:1234/aginx                     config from etcd.
`)
	cmd.PersistentFlags().StringP("expose", "e", "", "expose api use domain")
}

func AddServerFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringP("api", "a", ":8011", "restful api port")
	cmd.PersistentFlags().StringP("security", "s", "", "base auth for restful api, example: user:passwd")
	AddClusterFlag(cmd)
}

func init() {
	AddServerFlags(ServerCmd)
}