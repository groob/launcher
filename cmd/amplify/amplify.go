package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	netctx "golang.org/x/net/context"

	"cloud.google.com/go/pubsub"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	goog_uuid "github.com/google/uuid"
	"github.com/kolide/kit/env"
	"github.com/kolide/launcher/service"
	"github.com/kolide/osquery-go/plugin/logger"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// dialGRPC creates a grpc client connection.
func dialGRPC(
	serverURL string,
	insecureTLS bool,
	insecureGRPC bool,
	logger log.Logger,
) (*grpc.ClientConn, error) {
	level.Info(logger).Log(
		"msg", "dialing grpc server",
		"server", serverURL,
		"tls_secure", insecureTLS == false,
		"grpc_secure", insecureGRPC == false,
	)
	grpcOpts := []grpc.DialOption{
		grpc.WithTimeout(time.Second),
	}
	if insecureGRPC {
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
	} else {
		host, _, err := net.SplitHostPort(serverURL)
		if err != nil {
			return nil, errors.Wrapf(err, "split grpc server host and port: %s", serverURL)
		}
		creds := credentials.NewTLS(&tls.Config{
			ServerName:         host,
			InsecureSkipVerify: insecureTLS,
		})
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(creds))
	}
	conn, err := grpc.Dial(
		serverURL,
		grpcOpts...,
	)
	return conn, err
}

func logFatal(logger log.Logger, args ...interface{}) {
	level.Info(logger).Log(args...)
	os.Exit(1)
}

type extension struct {
	enrollSecret string
	serverURL    string
	logger       log.Logger
	psClient     *pubsub.Client
	topic        string
}

type hosts []*host

func (h hosts) Enroll(logger log.Logger, enrollSecret string) {
	var wg sync.WaitGroup
	wg.Add(len(h))
	for _, hs := range h {
		go func(hs *host) {
			if err := hs.RequestEnrollment(enrollSecret); err != nil {
				level.Info(logger).Log("err", err, "host", hs.Hostname)
			}
			wg.Done()
		}(hs)
	}
	wg.Wait()
	fmt.Println("done with enrolls")
}

func (h hosts) RequestConfig(logger log.Logger, enrollSecret string) {
	for _, hs := range h {
		if err := hs.RequestConfig(); err != nil {
			level.Info(logger).Log("err", err, "host", hs.Hostname)
		}
	}
}

func (h hosts) RequestQueries(logger log.Logger, enrollSecret string) {
	for _, hs := range h {
		if err := hs.RequestQueries(); err != nil {
			level.Info(logger).Log("err", err, "host", hs.Hostname)
		}
	}
}

func (h hosts) PublishLogs(logger log.Logger, logType logger.LogType, logs []string) {
	for _, hs := range h {
		if err := hs.PublishLogs(logType, logs); err != nil {
			level.Info(logger).Log("err", err, "host", hs.Hostname)
		}
	}
}

func (ext *extension) Run() error {
	var hosts hosts
	for i := 0; i <= 10; i++ {
		h := newHost(ext.serverURL, ext.logger)
		if h != nil {
			hosts = append(hosts, h)
			level.Info(ext.logger).Log("msg", "configured host", "serial", h.Serial, "hostname", h.Hostname)
		}
	}

	subscription := ext.topic + "consumer-group-1"
	topic := ext.psClient.Topic(ext.topic)
	ctx := context.Background()
	ext.psClient.CreateSubscription(ctx, subscription, pubsub.SubscriptionConfig{
		Topic:       topic,
		AckDeadline: 20 * time.Second,
	})

	s := ext.psClient.Subscription(subscription)
	err := s.Receive(ctx, func(ctx netctx.Context, msg *pubsub.Message) {
		method := msg.Attributes["method"]
		switch method {
		case "RequestEnrollment":
			hosts.Enroll(ext.logger, ext.enrollSecret)
		case "RequestConfig":
			hosts.RequestConfig(ext.logger, ext.enrollSecret)
		case "RequestQueries":
			hosts.RequestQueries(ext.logger, ext.enrollSecret)
		case "PublishLogs":
			var message = struct {
				Method  string         `json:"method"`
				LogType logger.LogType `json:"log_type"`
				Logs    []string       `json:"logs"`
			}{}
			if err := json.Unmarshal(msg.Data, &message); err != nil {
				level.Info(ext.logger).Log("err", err, "msg", "unmarshal PublishLogs msg")
			}
			var logs []string
			for _, l := range message.Logs {
				b64, err := base64.StdEncoding.DecodeString(l)
				if err != nil {
					level.Info(ext.logger).Log("err", err, "msg", "decode base64 log")
					continue
				}
				logs = append(logs, string(b64))
			}
			hosts.PublishLogs(ext.logger, message.LogType, logs)
		default:
			level.Info(ext.logger).Log("method", method)
		}
		msg.Ack()
	})
	if err != nil {
		return err
	}

	sig := make(chan os.Signal)
	signal.Notify(sig, os.Interrupt)
	<-sig
	return nil
}

type host struct {
	UUID     string
	NodeKey  string
	Hostname string
	config   string
	Serial   string

	serviceClient service.KolideService
	actionc       chan func()
	logger        log.Logger
}

func newHost(serverURL string, logger log.Logger) *host {
	h := &host{
		Hostname: fmt.Sprintf("fake-launcher-%d%d", rand.Intn(9), rand.Intn(9)),
		logger:   logger,
		actionc:  make(chan func()),
	}
	go h.loop()

	if err := h.setup(serverURL, logger); err != nil {
		level.Info(logger).Log("err", err, "msg", "host setup failed")
		return nil
	}

	return h
}

func (h *host) setup(serverURL string, logger log.Logger) error {
	errc := make(chan error, 1)
	h.actionc <- func() {
		h.Serial = randomMacSerial()
		nodekey, _ := ioutil.ReadFile(filepath.Join("/tmp", fmt.Sprintf("%s_nodekey", h.Hostname)))
		h.NodeKey = string(nodekey)
		conn, err := dialGRPC(serverURL, false, false, logger)
		if err != nil {
			errc <- err
			return
		}
		uuid, err := generateOrLoadUUID(h.Hostname)
		if err != nil {
			errc <- err
			return
		}
		h.serviceClient = service.New(conn, logger)
		h.UUID = uuid
		ioutil.WriteFile(filepath.Join("/tmp", h.Hostname), []byte(uuid), 0644)
		errc <- nil
	}
	return <-errc
}

func generateOrLoadUUID(hostname string) (string, error) {
	data, err := ioutil.ReadFile(filepath.Join("/tmp", hostname))
	if err == nil {
		return string(data), nil
	}
	uuid, err := goog_uuid.NewRandom()
	return uuid.String(), err
}

func (h *host) RequestEnrollment(enrollSecret string) error {
	errc := make(chan error, 1)
	h.actionc <- func() {
		var count int
		ctx := context.Background()
	authTag:
		nodekey, reauth, err := h.serviceClient.RequestEnrollment(ctx, enrollSecret, h.UUID)
		if err != nil {
			errc <- err
			return
		}
		if reauth && count < 1 {
			count++
			goto authTag
		}
		h.NodeKey = nodekey
		ioutil.WriteFile(filepath.Join("/tmp", fmt.Sprintf("%s_nodekey", h.Hostname)), []byte(nodekey), 0644)
		errc <- nil
	}
	return <-errc
}

func (h *host) RequestQueries() error {
	errc := make(chan error, 1)
	h.actionc <- func() {
		var count int
		ctx := context.Background()
	authTag:
		queries, reauth, err := h.serviceClient.RequestQueries(ctx, h.NodeKey)
		if err != nil {
			errc <- err
			return
		}
		if reauth && count < 1 {
			count++
			goto authTag
		}
		_ = queries
		errc <- nil
	}
	return <-errc
}

func (h *host) RequestConfig() error {
	errc := make(chan error, 1)
	h.actionc <- func() {
		var count int
		ctx := context.Background()
	authTag:
		config, reauth, err := h.serviceClient.RequestConfig(ctx, h.NodeKey)
		if err != nil {
			errc <- err
			return
		}
		if reauth && count < 1 {
			count++
			goto authTag
		}
		h.config = config
		errc <- nil
	}
	return <-errc
}

func (h *host) PublishLogs(logType logger.LogType, logs []string) error {
	var modifiedLogs []string
	for _, l := range logs {
		nl := strings.Replace(l, "FA01680E-98CA-5557-8F59-7716ECFEE964", h.UUID, -1)
		nl = strings.Replace(nl, "kl.groob.io", h.Hostname, -1)
		nl = strings.Replace(nl, "C02RX6G8G8WP", h.Serial, -1)
		nl = strings.Replace(nl, "amplify-launcher-identifier", h.UUID, -1)
		if strings.Compare(l, nl) != 0 {
			fmt.Println(nl)
		}
		modifiedLogs = append(modifiedLogs, nl)
	}
	errc := make(chan error, 1)
	h.actionc <- func() {
		var count int
		ctx := context.Background()
	authTag:
		_, _, reauth, err := h.serviceClient.PublishLogs(ctx, h.NodeKey, logType, modifiedLogs)
		if err != nil {
			errc <- err
			return
		}
		if reauth && count < 1 {
			count++
			goto authTag
		}
		errc <- nil
	}
	return <-errc
}

func (h *host) loop() {
	for {
		select {
		case f := <-h.actionc:
			f()
		}
	}
}

func randomMacSerial() string {
	source := "C02RX6G%d%d%d%d%d"
	newSerial := fmt.Sprintf(
		source,
		rand.Intn(9),
		rand.Intn(9),
		rand.Intn(9),
		rand.Intn(9),
		rand.Intn(9),
	)
	return newSerial

}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	var (
		flKolideServerURL = flag.String(
			"hostname",
			env.String("KOLIDE_LAUNCHER_HOSTNAME", ""),
			"Hostname of the Kolide server to communicate with",
		)
		flEnrollSecret = flag.String(
			"enroll_secret",
			env.String("KOLIDE_LAUNCHER_ENROLL_SECRET", ""),
			"Enroll secret to authenticate with the Kolide server",
		)
		flDebug   = flag.Bool("debug", false, "enable debug logging")
		flProject = flag.String(
			"gcp.project",
			env.String("GCP_PROJECT", "kolide-ose-testing"),
			"GCP Project Name",
		)
		flTopic = flag.String(
			"pubsub.topic",
			env.String("PUBSUB_TOPIC", "launcher.amplify"),
			"GCP Pubsub Topic",
		)
	)
	flag.Parse()

	logger := log.NewJSONLogger(os.Stderr)
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)
	if *flDebug {
		logger = level.NewFilter(logger, level.AllowDebug())
	} else {
		logger = level.NewFilter(logger, level.AllowInfo())
	}
	logger = log.With(logger, "caller", log.DefaultCaller)

	conn, err := dialGRPC(*flKolideServerURL, false, false, logger)
	if err != nil {
		logFatal(logger, "err", errors.Wrap(err, "dialing grpc server"))
	}
	defer conn.Close()

	psClient, err := pubsub.NewClient(context.Background(), *flProject)
	if err != nil {
		logFatal(logger, "err", err)
	}

	ext := &extension{
		enrollSecret: *flEnrollSecret,
		serverURL:    *flKolideServerURL,
		logger:       logger,
		psClient:     psClient,
		topic:        *flTopic,
	}
	if err := ext.Run(); err != nil {
		logFatal(logger, "err", err)
	}
}
