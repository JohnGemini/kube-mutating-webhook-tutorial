package main

import (
    "context"
    "flag"
    "crypto/tls"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    corev1 "k8s.io/api/core/v1"

    "github.com/golang/glog"
)

func main() {
    var ResourceGPU string
    flag.StringVar(&ResourceGPU, "gpu-alias", "nvidia.com/gpu",
                   "Alias name of the GPU device")
    flag.Parse()
    pair, err := tls.LoadX509KeyPair("/etc/webhook/certs/cert.pem",
                                     "/etc/webhook/certs/key.pem")
    if err != nil {
        glog.Errorf("Filed to load key pair: %v", err)
    }
    whsvr := &WebhookServer {
        server:           &http.Server {
            Addr:        ":443",
            TLSConfig:   &tls.Config{Certificates: []tls.Certificate{pair}},
        },
        ResourceCPU:      string(corev1.ResourceCPU),
        ResourceMemory:   string(corev1.ResourceMemory),
        ResourceGPU:      ResourceGPU,
    }

    // define http server and server handler
    mux := http.NewServeMux()
    mux.HandleFunc("/", whsvr.serve)
    whsvr.server.Handler = mux

    // start webhook server in new rountine
    go func() {
        if err := whsvr.server.ListenAndServeTLS("", ""); err != nil {
            glog.Errorf("Filed to listen and serve webhook server: %v", err)
        }
    }()

    // listening OS shutdown singal
    signalChan := make(chan os.Signal, 1)
    signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
    <-signalChan

    glog.Infof("Got OS shutdown signal, shutting down wenhook server gracefully...")
    whsvr.server.Shutdown(context.Background())
}
