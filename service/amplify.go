package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"

	"cloud.google.com/go/pubsub"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/kolide/osquery-go/plugin/distributed"
	"github.com/kolide/osquery-go/plugin/logger"
)

const (
	topic   = "launcher.amplify"
	project = "kolide-ose-testing"
)

func AmplifyMiddleware(logger log.Logger) func(KolideService) KolideService {
	return func(next KolideService) KolideService {
		ctx := context.Background()
		psClient, err := pubsub.NewClient(ctx, project)
		if err != nil {
			level.Info(logger).Log("err", err)
			os.Exit(1)
		}

		psClient.CreateTopic(ctx, topic)
		psTopic := psClient.Topic(topic)
		return amplifymw{logger, psClient, psTopic, next}
	}
}

type amplifymw struct {
	logger   log.Logger
	psClient *pubsub.Client
	topic    *pubsub.Topic
	next     KolideService
}

func (mw amplifymw) RequestEnrollment(ctx context.Context, enrollSecret, hostIdentifier string) (errcode string, reauth bool, err error) {
	var msg = struct {
		Method         string `json:"method"`
		EnrollSecret   string `json:"enroll_secret"`
		HostIdentifier string `json:"host_identifier"`
	}{
		EnrollSecret:   enrollSecret,
		HostIdentifier: hostIdentifier,
		Method:         "RequestEnrollment",
	}
	mw.publish(ctx, "RequestEnrollment", &msg)

	errcode, reauth, err = mw.next.RequestEnrollment(ctx, enrollSecret, hostIdentifier)
	return
}

func (mw amplifymw) RequestConfig(ctx context.Context, nodeKey string) (config string, reauth bool, err error) {
	var msg = struct {
		Method string `json:"method"`
	}{
		Method: "RequestConfig",
	}
	mw.publish(ctx, "RequestConfig", &msg)
	config, reauth, err = mw.next.RequestConfig(ctx, nodeKey)
	return
}

func (mw amplifymw) PublishLogs(ctx context.Context, nodeKey string, logType logger.LogType, logs []string) (message, errcode string, reauth bool, err error) {
	var base64logs []string
	for _, l := range logs {
		base64logs = append(base64logs, base64.StdEncoding.EncodeToString([]byte(l)))
	}
	var msg = struct {
		Method  string         `json:"method"`
		LogType logger.LogType `json:"log_type"`
		Logs    []string       `json:"logs"`
	}{
		Method:  "PublishLogs",
		LogType: logType,
		Logs:    base64logs,
	}
	mw.publish(ctx, "PublishLogs", &msg)

	message, errcode, reauth, err = mw.next.PublishLogs(ctx, nodeKey, logType, logs)
	return
}

func (mw amplifymw) RequestQueries(ctx context.Context, nodeKey string) (res *distributed.GetQueriesResult, reauth bool, err error) {
	var msg = struct {
		Method string `json:"method"`
	}{
		Method: "RequestQueries",
	}
	mw.publish(ctx, "RequestQueries", &msg)

	res, reauth, err = mw.next.RequestQueries(ctx, nodeKey)
	return
}

func (mw amplifymw) PublishResults(ctx context.Context, nodeKey string, results []distributed.Result) (message, errcode string, reauth bool, err error) {
	var msg = struct {
		Method  string               `json:"method"`
		Results []distributed.Result `json:"results"`
	}{
		Method:  "PublishResults",
		Results: results,
	}
	mw.publish(ctx, "PublishResults", &msg)

	message, errcode, reauth, err = mw.next.PublishResults(ctx, nodeKey, results)
	return
}

func (mw amplifymw) publish(ctx context.Context, method string, msg interface{}) {
	jsn, err := json.Marshal(msg)
	if err != nil {
		level.Info(mw.logger).Log("err", err, "msg", "marshal pubsub msg to json", "method", method)
		return
	}

	result := mw.topic.Publish(ctx, &pubsub.Message{
		Attributes: map[string]string{
			"method": method,
		},
		Data: jsn,
	})
	if _, err := result.Get(ctx); err != nil {
		level.Info(mw.logger).Log("err", err, "msg", "publish pubsub msg", "method", method)
		return
	}
}
