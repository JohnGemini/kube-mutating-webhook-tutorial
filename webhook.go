package main

import (
    "encoding/json"
    "fmt"
    "errors"
    "strings"
    "io/ioutil"
    "net/http"

    "github.com/golang/glog"
    "k8s.io/api/admission/v1beta1"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/api/resource"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
    runtimeScheme = runtime.NewScheme()
    codecs        = serializer.NewCodecFactory(runtimeScheme)
    deserializer  = codecs.UniversalDeserializer()
)

const (
    validateRequiredKey   = "desc"
    validateRequiredValue = "transparent mode namespace"
)

var ignoredNamespaces = []string {
    metav1.NamespaceSystem,
    metav1.NamespacePublic,
    metav1.NamespaceDefault,
}

type WebhookServer struct {
    server            *http.Server
    ResourceCPU       string
    ResourceGPU       string
    ResourceMemory    string
}

type patchOperation struct {
    Op    string      `json:"op"`
    Path  string      `json:"path"`
    Value interface{} `json:"value,omitempty"`
}

// Check whether the target need to be validated
func validationRequired(ignoredList []string, metadata *metav1.ObjectMeta) bool {
    // skip the child object of the other resource
    if ref := metadata.GetOwnerReferences(); len(ref) > 0 {
        glog.Infof("Skip validation for %v for it's a child object of %v/%v",
                   metadata.Name, ref[0].Kind, ref[0].Name)
        return false
    }
    // skip special kubernete system namespaces
    for _, namespace := range ignoredList {
        if metadata.Namespace == namespace {
            glog.Infof("Skip validation for %v for it's in special namespace: %v",
                       metadata.Name, metadata.Namespace)
            return false
        }
    }

    // get the in-cluster config
    config, err := rest.InClusterConfig()
    if err != nil {
        panic(err.Error())
    }
    // get a k8s client
    client, err := kubernetes.NewForConfig(config)
    if err != nil {
        panic(err.Error())
    }
    // get Namespace object by name
    namespace, err := client.CoreV1().Namespaces().Get(metadata.Namespace,
                                                       metav1.GetOptions{})
    if err != nil {
        panic(err.Error())
    }

    annotations := namespace.ObjectMeta.GetAnnotations()
    // determine whether to perform validation based on annotations of the target's namespace
    if val, ok := annotations[validateRequiredKey]; ok {
        if strings.Contains(val, validateRequiredValue) {
            return true
        }
    }
    glog.Infof("Skip validation for %v for there is no annotation %v='.* %v' in namespace %v",
               metadata.Name, validateRequiredKey,
               validateRequiredValue, metadata.Namespace)
    return false
}

// get PodSpec from the request
func getPodSpec(req *v1beta1.AdmissionRequest) (*corev1.PodSpec, error) {
    var (
        kind, name, namespace      string
        metadata                   *metav1.ObjectMeta
        spec                       *corev1.PodSpec
    )
    switch req.Kind.Kind {
        case "ReplicationController":
            var obj corev1.ReplicationController
            if err := json.Unmarshal(req.Object.Raw, &obj); err != nil {
                return nil, err
            }
            kind, name, namespace, metadata, spec = req.Kind.Kind, obj.Name,
                obj.Namespace, &obj.ObjectMeta, &obj.Spec.Template.Spec
        case "Pod":
            var obj corev1.Pod
            if err := json.Unmarshal(req.Object.Raw, &obj); err != nil {
                return nil, err
            }
            kind, name, namespace, metadata, spec = req.Kind.Kind, obj.Name,
                obj.Namespace, &obj.ObjectMeta, &obj.Spec
        default:
            return nil, nil
    }

    glog.Infof("AdmissionReview for Kind=%v Namespace=%v Name=%v",
               kind, namespace, name)

    // determine whether to perform validation
    if !validationRequired(ignoredNamespaces, metadata) {
        return nil, nil
    } else {
        return spec, nil
    }
}

func (whsvr *WebhookServer) limitsValidate(container *corev1.Container) (bool, error) {
    resourceLimits := map[string]float64 {
        whsvr.ResourceCPU:    0,
        whsvr.ResourceGPU:    0,
        whsvr.ResourceMemory: 0,
    }
    patchRequired := false

    if container.Resources.Limits != nil {
        limits := container.Resources.Limits
        for resourceName := range resourceLimits {
            if quantity, ok := limits[corev1.ResourceName(resourceName)]; ok {
                val := float64(quantity.Value())
                if resourceName == whsvr.ResourceMemory {
                    val /= (1024 * 1024)
                }
                resourceLimits[resourceName] += val
            }
        }
    } else {
        return patchRequired, errors.New("container[*].resources.limits is required")
    }

    limit_cpu := resourceLimits[whsvr.ResourceGPU] * 4
    limit_memory := resourceLimits[whsvr.ResourceGPU] * 90 * 1024
    if (resourceLimits[whsvr.ResourceGPU] <= 8 &&
        resourceLimits[whsvr.ResourceGPU] >= 1) {
        if resourceLimits[whsvr.ResourceCPU] > limit_cpu {
            return patchRequired, errors.New(fmt.Sprintf(
                "%v exceeds the limit of %v", whsvr.ResourceCPU, limit_cpu))
        } else if resourceLimits[whsvr.ResourceCPU] == 0 {
            resourceLimits[whsvr.ResourceCPU] = limit_cpu
            quantity, err := resource.ParseQuantity(fmt.Sprintf("%v", limit_cpu))
            if err != nil {
                return patchRequired, errors.New(fmt.Sprintf(
                    "Cannot parse value of %v: %v",
                    whsvr.ResourceGPU, limit_cpu))
            } else {
                container.Resources.Limits[corev1.ResourceName(whsvr.ResourceCPU)] =
                    quantity
                patchRequired = true
            }
        }
        if resourceLimits[whsvr.ResourceMemory] > limit_memory {
            return patchRequired, errors.New(fmt.Sprintf(
                "%v exceeds the limit of %v", whsvr.ResourceMemory, limit_memory))
        } else if resourceLimits[whsvr.ResourceMemory] == 0 {
            resourceLimits[whsvr.ResourceMemory] = limit_memory
            quantity, err := resource.ParseQuantity(fmt.Sprintf("%vMi", limit_memory))
            if err != nil {
                return patchRequired, errors.New(fmt.Sprintf(
                    "Cannot parse value of %v: %v",
                    whsvr.ResourceMemory, limit_memory))
            } else {
                container.Resources.Limits[corev1.ResourceName(whsvr.ResourceMemory)] =
                    quantity
                patchRequired = true
            }
        }
    } else {
        return patchRequired, errors.New(fmt.Sprintf(
            "%v exceeds the range from 1 to 8", whsvr.ResourceGPU))
    }
    glog.Infof("resourceLimits in container %v: %v",
               container.Name, resourceLimits)
    return patchRequired, nil
}

func (whsvr *WebhookServer) validateContainers(containers *[]corev1.Container, path string) ([]byte, error) {
    var patch []patchOperation
    patchRequired := false
    for _, container := range *containers {
        if pr, err := whsvr.limitsValidate(&container); err != nil {
            return nil, err
        } else {
            patchRequired = patchRequired || pr
        }
    }
    if patchRequired {
        patch = append(patch, patchOperation {
            Op:    "replace",
            Path:  path,
            Value: *containers,
        })
        return json.Marshal(patch)
    } else {
        return nil, nil
    }
}

// main validation process
func (whsvr *WebhookServer) validate(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
    var path string
    req := ar.Request
    switch req.Kind.Kind {
        case "ReplicationController":
            path = "/spec/template/spec/containers"
        case "Pod":
            path = "/spec/containers"
    }
    if spec, err := getPodSpec(req); err != nil {
        glog.Errorf("Could not unmarshal raw object: %v", err)
        return &v1beta1.AdmissionResponse {
            Result: &metav1.Status {
                Message: err.Error(),
            },
        }
    } else if spec != nil {
        if patchBytes, err := whsvr.validateContainers(&spec.Containers, path); err != nil {
            glog.Errorf("Validation fails: %v", err)
            return &v1beta1.AdmissionResponse {
                Result: &metav1.Status {
                    Message: err.Error(),
                },
            }
        } else if patchBytes != nil {
            return &v1beta1.AdmissionResponse {
                Allowed: true,
                Patch:   patchBytes,
                PatchType: func() *v1beta1.PatchType {
                    pt := v1beta1.PatchTypeJSONPatch
                    return &pt
                }(),
            }
        }
    }
    return &v1beta1.AdmissionResponse {
        Allowed: true,
    }
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
    var body []byte
    if r.Body != nil {
        if data, err := ioutil.ReadAll(r.Body); err == nil {
            body = data
        }
    }
    if len(body) == 0 {
        glog.Error("empty body")
        http.Error(w, "empty body", http.StatusBadRequest)
        return
    }

    // verify the content type is accurate
    contentType := r.Header.Get("Content-Type")
    if contentType != "application/json" {
        glog.Errorf("Content-Type=%s, expect application/json", contentType)
        http.Error(w, "invalid Content-Type, expect `application/json`",
                   http.StatusUnsupportedMediaType)
        return
    }

    var admissionResponse *v1beta1.AdmissionResponse
    ar := v1beta1.AdmissionReview{}
    if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
        glog.Errorf("Can't decode body: %v", err)
        admissionResponse = &v1beta1.AdmissionResponse {
            Result: &metav1.Status {
                Message: err.Error(),
            },
        }
    } else {
        admissionResponse = whsvr.validate(&ar)
    }

    admissionReview := v1beta1.AdmissionReview{}
    if admissionResponse != nil {
        admissionReview.Response = admissionResponse
        if ar.Request != nil {
            admissionReview.Response.UID = ar.Request.UID
        }
    }

    resp, err := json.Marshal(admissionReview)
    if err != nil {
        glog.Errorf("Can't encode response: %v", err)
        http.Error(w, fmt.Sprintf("could not encode response: %v", err),
                   http.StatusInternalServerError)
    }
    glog.Infof("Ready to write reponse ...")
    if _, err := w.Write(resp); err != nil {
        glog.Errorf("Can't write response: %v", err)
        http.Error(w, fmt.Sprintf("could not write response: %v", err),
                   http.StatusInternalServerError)
    }
}
