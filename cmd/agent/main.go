/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"k8s.io/klog"
	"sigs.k8s.io/apiserver-network-proxy/pkg/agent/agentclient"
	"sigs.k8s.io/apiserver-network-proxy/pkg/util"
)

func main() {
	agent := &Agent{}
	o := newGrpcProxyAgentOptions()
	command := newAgentCommand(agent, o)
	flags := command.Flags()
	flags.AddFlagSet(o.Flags())
	local := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	klog.InitFlags(local)
	local.VisitAll(func(fl *flag.Flag) {
		fl.Name = util.Normalize(fl.Name)
		flags.AddGoFlag(fl)
	})
	if err := command.Execute(); err != nil {
		klog.Errorf("error: %v\n", err)
		klog.Flush()
		os.Exit(1)
	}
}

type GrpcProxyAgentOptions struct {
	// Configuration for authenticating with the proxy-server
	agentCert string
	agentKey  string
	caCert    string

	// Configuration for connecting to the proxy-server
	proxyServerHost string
	proxyServerPort int
}

func (o *GrpcProxyAgentOptions) Flags() *pflag.FlagSet {
	flags := pflag.NewFlagSet("proxy-agent", pflag.ContinueOnError)
	flags.StringVar(&o.agentCert, "agent-cert", o.agentCert, "If non-empty secure communication with this cert.")
	flags.StringVar(&o.agentKey, "agent-key", o.agentKey, "If non-empty secure communication with this key.")
	flags.StringVar(&o.caCert, "ca-cert", o.caCert, "If non-empty the CAs we use to validate clients.")
	flags.StringVar(&o.proxyServerHost, "proxy-server-host", o.proxyServerHost, "The hostname to use to connect to the proxy-server.")
	flags.IntVar(&o.proxyServerPort, "proxy-server-port", o.proxyServerPort, "The port the proxy server is listening on.")
	return flags
}

func (o *GrpcProxyAgentOptions) Print() {
	klog.Warningf("AgentCert set to \"%s\".\n", o.agentCert)
	klog.Warningf("AgentKey set to \"%s\".\n", o.agentKey)
	klog.Warningf("CACert set to \"%s\".\n", o.caCert)
	klog.Warningf("ProxyServerHost set to \"%s\".\n", o.proxyServerHost)
	klog.Warningf("ProxyServerPort set to %d.\n", o.proxyServerPort)
}

func (o *GrpcProxyAgentOptions) Validate() error {
	if o.agentKey != "" {
		if _, err := os.Stat(o.agentKey); os.IsNotExist(err) {
			return fmt.Errorf("error checking agent key %s, got %v", o.agentKey, err)
		}
		if o.agentCert == "" {
			return fmt.Errorf("cannot have agent cert empty when agent key is set to \"%s\"", o.agentKey)
		}
	}
	if o.agentCert != "" {
		if _, err := os.Stat(o.agentCert); os.IsNotExist(err) {
			return fmt.Errorf("error checking agent cert %s, got %v", o.agentCert, err)
		}
		if o.agentKey == "" {
			return fmt.Errorf("cannot have agent key empty when agent cert is set to \"%s\"", o.agentCert)
		}
	}
	if o.caCert != "" {
		if _, err := os.Stat(o.caCert); os.IsNotExist(err) {
			return fmt.Errorf("error checking agent CA cert %s, got %v", o.caCert, err)
		}
	}
	if o.proxyServerPort <= 0 {
		return fmt.Errorf("proxy server port %d must be greater than 0", o.proxyServerPort)
	}
	return nil
}

func newGrpcProxyAgentOptions() *GrpcProxyAgentOptions {
	o := GrpcProxyAgentOptions{
		agentCert:       "",
		agentKey:        "",
		caCert:          "",
		proxyServerHost: "127.0.0.1",
		proxyServerPort: 8091,
	}
	return &o
}

func newAgentCommand(a *Agent, o *GrpcProxyAgentOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "agent",
		Long: `A gRPC agent, Connects to the proxy and then allows traffic to be forwarded to it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(o)
		},
	}

	return cmd
}

type Agent struct {
}

func (a *Agent) run(o *GrpcProxyAgentOptions) error {
	o.Print()
	if err := o.Validate(); err != nil {
		return fmt.Errorf("failed to validate agent options with %v", err)
	}

	if err := a.runProxyConnection(o); err != nil {
		return fmt.Errorf("failed to run proxy connection with %v", err)
	}

	if err := a.runAdminServer(o); err != nil {
		return fmt.Errorf("failed to run admin server with %v", err)
	}

	stopCh := make(chan struct{})
	<-stopCh

	return nil
}

func (a *Agent) runProxyConnection(o *GrpcProxyAgentOptions) error {
	var tlsConfig *tls.Config
	var err error
	if tlsConfig, err = util.GetClientTLSConfig(o.caCert, o.agentCert, o.agentKey, o.proxyServerHost); err != nil {
		return err
	}
	dialOption := grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))
	client, err := agentclient.NewAgentClient(fmt.Sprintf("%s:%d", o.proxyServerHost, o.proxyServerPort), dialOption)
	if err != nil {
		return err
	}

	stopCh := make(chan struct{})

	go client.Serve(stopCh)

	return nil
}

func (a *Agent) runAdminServer(o *GrpcProxyAgentOptions) error {
	livenessHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})
	readinessHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prometheus.Handler().ServeHTTP(w, r)
	})

	muxHandler := http.NewServeMux()
	muxHandler.HandleFunc("/healthz", livenessHandler)
	muxHandler.HandleFunc("/ready", readinessHandler)
	muxHandler.HandleFunc("/metrics", metricsHandler)
	adminServer := &http.Server{
		Addr:           "127.0.0.1:8093",
		Handler:        muxHandler,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		err := adminServer.ListenAndServe()
		if err != nil {
			klog.Warningf("health server received %v.\n", err)
		}
		klog.Warningf("Health server stopped listening\n")
	}()

	return nil
}
