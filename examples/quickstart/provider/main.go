/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	host      string
	port      int
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "EchoServerGolang", "service")
	flag.IntVar(&port, "port", 7879, "port")
}

// PolarisProvider .
type PolarisProvider struct {
	provider  api.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run . execute
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if nil != err {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	host = tmpHost
	svr.registerService()
	svr.runWebServer()
}

func (svr *PolarisProvider) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("Hello, I'm EchoServerGolang Provider"))
	})

	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", svr.port), nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &api.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = host
	registerRequest.Port = port
	registerRequest.ServiceToken = "token"
	registerRequest.SetTTL(10)
	resp, err := svr.provider.Register(registerRequest)
	if nil != err {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)

	go svr.doHeartbeat()
}

func (svr *PolarisProvider) doHeartbeat() {
	log.Printf("start to invoke heartbeat operation")
	ticker := time.NewTicker(time.Duration(10 * time.Second))
	for range ticker.C {
		heartbeatRequest := &api.InstanceHeartbeatRequest{}
		heartbeatRequest.Namespace = namespace
		heartbeatRequest.Service = service
		heartbeatRequest.Host = host
		heartbeatRequest.Port = port
		heartbeatRequest.ServiceToken = "token"
		err := svr.provider.Heartbeat(heartbeatRequest)
		if nil != err {
			log.Fatalf("fail to heartbeat instance, err is %v", err)
		}
		time.Sleep(2 * time.Second)
	}

}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	provider, err := api.NewProviderAPI()
	if nil != err {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer provider.Destroy()

	svr := &PolarisProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
		host:      host,
		port:      port,
	}

	svr.Run()
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if nil != err {
		return "", err
	}
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}
