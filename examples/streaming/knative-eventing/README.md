# Seldon Core Real Time Stream Processing with KNative Eventing 

In this example we will show how you can enable real time stream processing in Seldon Core by leveraging the KNative Eventing integration.

In this example we will deploy a simple model containerised with Seldon Core and we will leverage the basic Seldon Core  integration with KNative Eventing which will allow us to connect it so it can receive cloud events as requests and return a cloudevent-enabled response which can be collected by other components.

## Pre-requisites

You will require the following in order to go ahead:
* Istio 1.42+ Installed ([Documentation Instructions](https://istio.io/docs/setup/install/helm/))
* KNative Eventing 0.13 installed ([Documentation Instructions](https://knative.dev/docs/install/any-kubernetes-cluster/))
* Seldon Core v1.1+ installed with Istio Ingress Enabled ([Documentation Instructions](https://docs.seldon.io/projects/seldon-core/en/latest/workflow/install.html#ingress-support))

## Deploy your Seldon Model

We will first deploy our model using Seldon Core. In this case we'll use one of the [pre-packaged model servers](https://docs.seldon.io/projects/seldon-core/en/latest/servers/overview.html).

We first createa  configuration file:


```python
%%writefile ./assets/simple-iris-deployment.yaml

apiVersion: machinelearning.seldon.io/v1
kind: SeldonDeployment
metadata:
  name: simple-iris-deployment
spec:
  name: simple-iris-spec
  predictors:
  - graph:
      implementation: SKLEARN_SERVER
      modelUri: gs://seldon-models/sklearn/iris
      name: simple-iris-model
      children: []
    name: simple-iris-predictor
    replicas: 1

```

    Overwriting ./assets/simple-iris-deployment.yaml


### Run the model in our cluster

Now we run the Seldon Deployment configuration file we just created.


```python
!kubectl apply -f assets/simple-iris-deployment.yaml
```

    seldondeployment.machinelearning.seldon.io/simple-iris-deployment created


### Check that the model has been deployed




```python
!kubectl get pods
```

    NAME                                                              READY   STATUS    RESTARTS   AGE
    default-broker-filter-7ffddb5dcc-pdbdt                            1/1     Running   1          7d8h
    default-broker-ingress-5cfc4c8cbc-dqr82                           1/1     Running   1          7d8h
    seldon-controller-manager-5f8cfb4648-jfltq                        1/1     Running   0          163m
    simple-iris-deployment-simple-iris-predictor-0-6dd54c578c-6gc6p   2/2     Running   0          6m58s


## Create a Trigger to reach our model 

We want to create a trigger that is able to reach directly to the service.

We will be using the following seldon deployment:


```python
!kubectl get sdep
```

    NAME                     AGE
    simple-iris-deployment   3m22s


### Create trigger configuration


```python
%%writefile ./assets/seldon-knative-trigger.yaml

apiVersion: eventing.knative.dev/v1beta1
kind: Trigger
metadata:
  name: seldon-eventing-sklearn-trigger
spec:
  broker: default
  filter:
    attributes:
      type: seldon.simple-iris-deployment.request
  subscriber:
    ref: 
      apiVersion: machinelearning.seldon.io/v1
      kind: SeldonDeployment
      name: simple-iris-deployment

```

    Overwriting ./assets/seldon-knative-trigger.yaml


Create this trigger file which will send all cloudevents of type `"seldon.<deploymentName>.request"`.


```python
!kubectl apply -f assets/seldon-knative-trigger.yaml
```

    trigger.eventing.knative.dev/seldon-eventing-sklearn-trigger unchanged


CHeck that the trigger is working correctly (you should see "Ready: True"), together with the URL that will be reached.


```python
!kubectl get trigger 
```

    NAME                              READY   REASON   BROKER    SUBSCRIBER_URI                                                                                                          AGE
    event-display                     True             default   http://event-display.default.svc.cluster.local/                                                                         3d5h
    seldon-eventing-sklearn-trigger   True             default   http://istio-ingressgateway.istio-system.svc.cluster.local/seldon/default/simple-iris-deployment/api/v1.0/predictions   74m


### Send a request to the KNative Eventing default broker

To send requests we can do so by sending a curl command from a pod inside of the cluster.


```python
!kubectl run --quiet=true -it --rm curl --image=radial/busyboxplus:curl --restart=Never -- \
    curl -v "default-broker.default.svc.cluster.local" \
        -H "Ce-Id: 536808d3-88be-4077-9d7a-a3f162705f79" \
        -H "Ce-specversion: 0.3" \
        -H "Ce-Type: seldon.simple-iris-deployment.request" \
        -H "Ce-Source: seldon.examples.streaming.curl" \
        -H "Content-Type: application/json" \
        -d '{"data": { "ndarray": [[1,2,3,4]]}}'
```

    
    
    
    
    
    
    
    
    
    
    
    
    
    
    


### Check our model has received it

We can do this by checking the logs (we can query the logs through the service name) and see that the request has been processed


```python
!kubectl logs svc/simple-iris-deployment-simple-iris-predictor simple-iris-model | tail -6
```

    2020-03-21 03:39:34,169 - SKLearnServer:predict:37 - INFO:  Calling predict_proba
    2020-03-21 03:39:34,170 - werkzeug:_log:122 - INFO:  127.0.0.1 - - [21/Mar/2020 03:39:34] "[37mPOST /predict HTTP/1.1[0m" 200 -
    2020-03-21 03:41:02,604 - SKLearnServer:predict:37 - INFO:  Calling predict_proba
    2020-03-21 03:41:02,605 - werkzeug:_log:122 - INFO:  127.0.0.1 - - [21/Mar/2020 03:41:02] "[37mPOST /predict HTTP/1.1[0m" 200 -
    2020-03-21 03:41:28,590 - SKLearnServer:predict:37 - INFO:  Calling predict_proba
    2020-03-21 03:41:28,591 - werkzeug:_log:122 - INFO:  127.0.0.1 - - [21/Mar/2020 03:41:28] "[37mPOST /predict HTTP/1.1[0m" 200 -


## Connect a source to listen to the results of the seldon model

Our Seldon Model is producing results which are sent back to KNative.

This means that we can connect other subsequent services through a trigger that filters for those response cloudevents.

### First create the service that willl print the results

This is just a simple pod that prints all the request data into the console.


```python
%%writefile ./assets/event-display-deployment.yaml

# event-display app deploment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: event-display
spec:
  replicas: 1
  selector:
    matchLabels: &labels
      app: event-display
  template:
    metadata:
      labels: *labels
    spec:
      containers:
        - name: helloworld-python
          image: gcr.io/knative-releases/github.com/knative/eventing-sources/cmd/event_display
---
# Service that exposes event-display app.
# This will be the subscriber for the Trigger
kind: Service
apiVersion: v1
metadata:
  name: event-display
spec:
  selector:
    app: event-display
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080

```

    Overwriting ./assets/event-display-deployment.yaml


### Now run the event display resources


```python
!kubectl apply -f assets/event-display-deployment.yaml
```

    deployment.apps/event-display unchanged
    service/event-display unchanged


### Check that the event display has been deployed


```python
!kubectl get pods | grep event
```

    event-display-7c69959598-dr7fb                                    1/1     Running   0          3d4h


### Create trigger for event display

We now can create a trigger that sends all the requests of the type and source created by the seldon deployment to our event display pod


```python
%%writefile ./assets/event-display-trigger.yaml

# Trigger to send events to service above
apiVersion: eventing.knative.dev/v1alpha1
kind: Trigger
metadata:
  name: event-display
spec:
  broker: default
  filter:
    attributes:
      type: seldon.simple-iris-deployment.response
      source: seldon.simple-iris-deployment
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: event-display

```

    Overwriting ./assets/event-display-trigger.yaml


### Apply that trigger


```python
!kubectl apply -f assets/event-display-trigger.yaml
```

    trigger.eventing.knative.dev/event-display configured


### Check our triggers are correctly set up

We now should see the event trigger available.


```python
!kubectl get trigger
```

    NAME                              READY   REASON   BROKER    SUBSCRIBER_URI                                                                                                          AGE
    event-display                     True             default   http://event-display.default.svc.cluster.local/                                                                         3d5h
    seldon-eventing-sklearn-trigger   True             default   http://istio-ingressgateway.istio-system.svc.cluster.local/seldon/default/simple-iris-deployment/api/v1.0/predictions   73m


## Send a couple of requests more

We can use the same process we outlined above to send a couple more events.



```python
!kubectl run --quiet=true -it --rm curl --image=radial/busyboxplus:curl --restart=Never -- \
    curl -v "default-broker.default.svc.cluster.local" \
        -H "Ce-Id: 536808d3-88be-4077-9d7a-a3f162705f79" \
        -H "Ce-specversion: 0.3" \
        -H "Ce-Type: seldon.simple-iris-deployment.request" \
        -H "Ce-Source: dev.knative.samples/helloworldsource" \
        -H "Content-Type: application/json" \
        -d '{"data": { "ndarray": [[1,2,3,4]]}}'
```

    
    
    
    
    
    
    
    
    
    
    
    
    
    
    


### Visualise the requests that come from the service


```python
!kubectl logs svc/event-display | tail -30
```

      specversion: 0.3
      type: seldon.simple-iris-deployment.response
      source: seldon.simple-iris-deployment
      id: a41b4e2a-6db3-4d96-9532-29e8db1f5cef
      time: 2020-03-21T03:41:28.593036193Z
      datacontenttype: application/json
    Extensions,
      knativearrivaltime: 2020-03-21T03:41:28.594071893Z
      knativehistory: default-kne-trigger-kn-channel.default.svc.cluster.local
      path: /api/v1.0/predictions
      traceparent: 00-1a93791481a6b73a6d59b48a96e62ee6-2bac18e237bba0a5-00
    Data,
      {
        "data": {
          "names": [
            "t:0",
            "t:1",
            "t:2"
          ],
          "ndarray": [
            [
              0.0006985194531162841,
              0.003668039039435755,
              0.9956334415074478
            ]
          ]
        },
        "meta": {}
      }
    



```python

```