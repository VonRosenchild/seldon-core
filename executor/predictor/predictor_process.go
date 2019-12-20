package predictor

import (
	"github.com/go-logr/logr"
	"github.com/seldonio/seldon-core/executor/api/client"
	"github.com/seldonio/seldon-core/executor/api/payload"
	"github.com/seldonio/seldon-core/operator/apis/machinelearning/v1"
	"sync"
)

type PredictorProcess struct {
	Client client.SeldonApiClient
	Log    logr.Logger
}

func hasMethod(method v1.PredictiveUnitMethod, methods *[]v1.PredictiveUnitMethod) bool {
	if methods != nil {
		for _, m := range *methods {
			if m == method {
				return true
			}
		}
	}
	return false
}

func (p *PredictorProcess) transformInput(node *v1.PredictiveUnit, msg payload.SeldonPayload) (payload.SeldonPayload, error) {
	if (*node).Type != nil {
		switch *node.Type {
		case v1.MODEL:
			return p.Client.Predict(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
		case v1.TRANSFORMER:
			return p.Client.TransformInput(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
		}
	}
	if hasMethod(v1.TRANSFORM_INPUT, node.Methods) {
		return p.Client.TransformInput(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
	}
	return msg, nil
}

func (p *PredictorProcess) transformOutput(node *v1.PredictiveUnit, msg payload.SeldonPayload) (payload.SeldonPayload, error) {
	if (*node).Type != nil {
		switch *node.Type {
		case v1.OUTPUT_TRANSFORMER:
			return p.Client.TransformOutput(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
		}
	}
	if hasMethod(v1.TRANSFORM_OUTPUT, node.Methods) {
		return p.Client.TransformOutput(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
	}
	return msg, nil
}

func (p *PredictorProcess) route(node *v1.PredictiveUnit, msg payload.SeldonPayload) (int, error) {
	if (*node).Type != nil {
		switch *node.Type {
		case v1.ROUTER:
			return p.Client.Route(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
		}
	}
	if hasMethod(v1.ROUTE, node.Methods) {
		return p.Client.Route(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
	}
	if node.Implementation != nil && *node.Implementation == v1.RANDOM_ABTEST {
		return p.abTestRouter(node)
	}
	return -1, nil
}

func (p *PredictorProcess) aggregate(node *v1.PredictiveUnit, msg []payload.SeldonPayload) (payload.SeldonPayload, error) {
	if (*node).Type != nil {
		switch *node.Type {
		case v1.COMBINER:
			return p.Client.Combine(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
		}
	}
	if hasMethod(v1.AGGREGATE, node.Methods) {
		return p.Client.Combine(node.Endpoint.ServiceHost, node.Endpoint.ServicePort, msg)
	}
	return msg[0], nil
}

func (p *PredictorProcess) routeChildren(node *v1.PredictiveUnit, msg payload.SeldonPayload) (payload.SeldonPayload, error) {
	if node.Children != nil && len(node.Children) > 0 {
		route, err := p.route(node, msg)
		if err != nil {
			return nil, err
		}
		var cmsgs []payload.SeldonPayload
		if route == -1 {
			cmsgs = make([]payload.SeldonPayload, len(node.Children))
			var errs = make([]error, len(node.Children))
			wg := sync.WaitGroup{}
			for i, nodeChild := range node.Children {
				wg.Add(1)
				go func(i int, nodeChild v1.PredictiveUnit, msg payload.SeldonPayload) {
					cmsgs[i], errs[i] = p.Execute(&nodeChild, msg)
					wg.Done()
				}(i, nodeChild, msg)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					return cmsgs[i], err
				}
			}
		} else {
			cmsgs = make([]payload.SeldonPayload, 1)
			cmsgs[0], err = p.Execute(&node.Children[route], msg)
			if err != nil {
				return cmsgs[0], err
			}
		}
		return p.aggregate(node, cmsgs)
	} else {
		return msg, nil
	}
}

func (p *PredictorProcess) Execute(node *v1.PredictiveUnit, msg payload.SeldonPayload) (payload.SeldonPayload, error) {
	tmsg, err := p.transformInput(node, msg)
	if err != nil {
		return tmsg, err
	}
	cmsg, err := p.routeChildren(node, tmsg)
	if err != nil {
		return tmsg, err
	}
	return p.transformOutput(node, cmsg)
}