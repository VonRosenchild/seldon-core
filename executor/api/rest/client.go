package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/golang/protobuf/jsonpb"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/seldonio/seldon-core/executor/api/client"
	"github.com/seldonio/seldon-core/executor/api/grpc/seldon"
	"github.com/seldonio/seldon-core/executor/api/grpc/seldon/proto"
	"github.com/seldonio/seldon-core/executor/api/metric"
	"github.com/seldonio/seldon-core/executor/api/payload"
	v1 "github.com/seldonio/seldon-core/operator/apis/machinelearning/v1"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"strconv"
	"strings"
)

const (
	ContentTypeJSON = "application/json"
)

type JSONRestClient struct {
	httpClient     *http.Client
	Log            logr.Logger
	Protocol       string
	DeploymentName string
	predictor      *v1.PredictorSpec
	metrics        *metric.ClientMetrics
}

func (smc *JSONRestClient) CreateErrorPayload(err error) payload.SeldonPayload {
	respFailed := proto.SeldonMessage{Status: &proto.Status{Code: http.StatusInternalServerError, Info: err.Error()}}
	m := jsonpb.Marshaler{}
	jStr, _ := m.MarshalToString(&respFailed)
	res := payload.BytesPayload{Msg: []byte(jStr)}
	return &res
}

func (smc *JSONRestClient) Marshall(w io.Writer, msg payload.SeldonPayload) error {
	_, err := w.Write(msg.GetPayload().([]byte))
	return err
}

func (smc *JSONRestClient) Unmarshall(msg []byte) (payload.SeldonPayload, error) {
	reqPayload := payload.BytesPayload{Msg: msg, ContentType: ContentTypeJSON}
	return &reqPayload, nil
}

type BytesRestClientOption func(client *JSONRestClient)

func NewJSONRestClient(protocol string, deploymentName string, predictor *v1.PredictorSpec, options ...BytesRestClientOption) client.SeldonApiClient {

	client := JSONRestClient{
		http.DefaultClient,
		logf.Log.WithName("JSONRestClient"),
		protocol,
		deploymentName,
		predictor,
		metric.NewClientMetrics(predictor, deploymentName, ""),
	}
	for i := range options {
		options[i](&client)
	}

	return &client
}

func (smc *JSONRestClient) getMetricsRoundTripper(modelName string, service string) http.RoundTripper {
	container := v1.GetContainerForPredictiveUnit(smc.predictor, modelName)
	imageName := ""
	imageVersion := ""
	if container != nil {
		imageParts := strings.Split(container.Image, ":")
		imageName = imageParts[0]
		if len(imageParts) == 2 {
			imageVersion = imageParts[1]
		}
	}
	return promhttp.InstrumentRoundTripperDuration(smc.metrics.ClientHandledHistogram.MustCurryWith(prometheus.Labels{
		metric.DeploymentNameMetric:   smc.DeploymentName,
		metric.PredictorNameMetric:    smc.predictor.Name,
		metric.PredictorVersionMetric: smc.predictor.Annotations["version"],
		metric.ServiceMetric:          service,
		metric.ModelNameMetric:        modelName,
		metric.ModelImageMetric:       imageName,
		metric.ModelVersionMetric:     imageVersion,
	}), http.DefaultTransport)
}

func (smc *JSONRestClient) PostHttp(ctx context.Context, modelName string, method string, url *url.URL, msg []byte) ([]byte, string, error) {
	smc.Log.Info("Calling HTTP", "URL", url)

	req, err := http.NewRequest("POST", url.String(), bytes.NewBuffer(msg))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", ContentTypeJSON)

	if opentracing.IsGlobalTracerRegistered() {
		tracer := opentracing.GlobalTracer()

		parentSpan := opentracing.SpanFromContext(ctx)
		clientSpan := opentracing.StartSpan(
			method,
			opentracing.ChildOf(parentSpan.Context()))
		defer clientSpan.Finish()
		tracer.Inject(clientSpan.Context(), opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(req.Header))
	}

	client := smc.httpClient
	client.Transport = smc.getMetricsRoundTripper(modelName, method)

	response, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}

	if response.StatusCode != http.StatusOK {
		smc.Log.Info("httpPost failed", "response code", response.StatusCode)
		return nil, "", errors.Errorf("Internal service call failed with to %s status code %d", url, response.StatusCode)
	}

	//Read response
	b, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()

	contentType := response.Header.Get("Content-Type")

	return b, contentType, nil
}

func (smc *JSONRestClient) getMethod(method string, modelName string) string {
	if smc.Protocol == ProtocolSeldon {
		return method
	}
	switch method {
	case client.SeldonPredictPath, client.SeldonTransformInputPath, client.SeldonTransformOutputPath:
		return "/v1/models/" + modelName + ":predict"
	case client.SeldonCombinePath:
		return "/v1/models/" + modelName + ":aggregate"
	case client.SeldonRoutePath:
		return "/v1/models/" + modelName + ":route"
	}
	return method
}

func (smc *JSONRestClient) call(ctx context.Context, modelName string, method string, host string, port int32, req payload.SeldonPayload) (payload.SeldonPayload, error) {
	url := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, strconv.Itoa(int(port))),
		Path:   method,
	}
	sm, contentType, err := smc.PostHttp(ctx, modelName, method, &url, req.GetPayload().([]byte))
	if err != nil {
		return nil, err
	}
	res := payload.BytesPayload{Msg: sm, ContentType: contentType}
	return &res, nil
}

func (smc *JSONRestClient) Chain(ctx context.Context, modelName string, msg payload.SeldonPayload) (payload.SeldonPayload, error) {
	switch smc.Protocol {
	case ProtocolSeldon: // Seldon Messages can always be chained together
		return msg, nil
	case ProtocolTensorflow: // Attempt to chain tensorflow payload
		return ChainTensorflow(msg)
	}
	return nil, errors.Errorf("Unknown protocol %s", smc.Protocol)
}

func (smc *JSONRestClient) Predict(ctx context.Context, modelName string, host string, port int32, req payload.SeldonPayload) (payload.SeldonPayload, error) {
	return smc.call(ctx, modelName, smc.getMethod(client.SeldonPredictPath, modelName), host, port, req)
}

func (smc *JSONRestClient) TransformInput(ctx context.Context, modelName string, host string, port int32, req payload.SeldonPayload) (payload.SeldonPayload, error) {
	return smc.call(ctx, modelName, smc.getMethod(client.SeldonTransformInputPath, modelName), host, port, req)
}

// Try to extract from SeldonMessage otherwise fall back to extract from Json Array
func (smc *JSONRestClient) Route(ctx context.Context, modelName string, host string, port int32, req payload.SeldonPayload) (int, error) {
	sp, err := smc.call(ctx, modelName, smc.getMethod(client.SeldonRoutePath, modelName), host, port, req)
	if err != nil {
		return 0, err
	} else {
		var routes []int
		msg := sp.GetPayload().([]byte)

		var sm proto.SeldonMessage
		value := string(msg)
		err := jsonpb.UnmarshalString(value, &sm)
		if err == nil {
			//Remove in future
			routes = seldon.ExtractRouteFromSeldonMessage(&sm)
		} else {
			routes, err = ExtractRouteAsJsonArray(msg)
			if err != nil {
				return 0, err
			}
		}

		//Only returning first route. API could be extended to allow multiple routes
		return routes[0], nil
	}
}

func isJSON(data []byte) bool {
	var js json.RawMessage
	return json.Unmarshal(data, &js) == nil
}

func (smc *JSONRestClient) Combine(ctx context.Context, modelName string, host string, port int32, msgs []payload.SeldonPayload) (payload.SeldonPayload, error) {
	// Extract into string array checking the data is JSON
	strData := make([]string, len(msgs))
	for i, sm := range msgs {
		if !isJSON(sm.GetPayload().([]byte)) {
			return nil, fmt.Errorf("Data is not JSON")
		} else {
			strData[i] = string(sm.GetPayload().([]byte))
		}
	}
	// Create JSON list of messages
	joined := strings.Join(strData, ",")
	jStr := "[" + joined + "]"
	req := payload.BytesPayload{Msg: []byte(jStr)}
	return smc.call(ctx, modelName, smc.getMethod(client.SeldonCombinePath, modelName), host, port, &req)
}

func (smc *JSONRestClient) TransformOutput(ctx context.Context, modelName string, host string, port int32, req payload.SeldonPayload) (payload.SeldonPayload, error) {
	return smc.call(ctx, modelName, smc.getMethod(client.SeldonTransformOutputPath, modelName), host, port, req)
}