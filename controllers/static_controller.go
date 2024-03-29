/*
Copyright 2023.

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

package controllers

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webpagev1alpha1 "github.com/DeamonMV/static-webpage-k8s-operator/api/v1alpha1"
)

// Definitions to manage status conditions
const (
	typeAvailableWebpage = "Available"
	typeErrorWebpage     = "Error"
	typeUnknownWebpage   = "Unknown"
	nginxServeFolder     = "/usr/share/nginx"
	nginxServeFolderLink = "/usr/share/nginx/html"
	constGitsyncImage    = "registry.k8s.io/git-sync/git-sync:v3.6.4"
	constNginxImage      = "nginx:1.23.3"
)

// StaticReconciler reconciles a Static object
type StaticReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// The following markers are used to generate the rules permissions (RBAC) on config/rbac using controller-gen
// when the command <make manifests> is executed.
// To know more about markers see: https://book.kubebuilder.io/reference/markers.html

//+kubebuilder:rbac:groups=webpage.daemon.io,resources=statics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=webpage.daemon.io,resources=statics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Static object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// It is essential for the controller's reconciliation loop to be idempotent. By following the Operator
// pattern you will create Controllers which provide a reconcile function
// responsible for synchronizing resources until the desired state is reached on the cluster.
// Breaking this recommendation goes against the design principles of controller-runtime.
// and may lead to unforeseen consequences such as resources becoming stuck and requiring manual intervention.
// For further info:
// - About Operator Pattern: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
// - About Controllers: https://kubernetes.io/docs/concepts/architecture/controller/
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile

func (r *StaticReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("RECONCILE")
	log.Info(time.Now().String())
	// Fetch the Webpage instance
	// The purpose is check if the Custom Resource for the Kind Webpage
	// is applied on the cluster if not we return nil to stop the reconciliation
	webpage := &webpagev1alpha1.Static{}
	err := r.Get(ctx, req.NamespacedName, webpage)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("webpage resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get webpage")
		return ctrl.Result{}, err
	}
	// Let's just set the status as Unknown when no status are available
	if webpage.Status.Conditions == nil || len(webpage.Status.Conditions) == 0 {
		meta.SetStatusCondition(
			&webpage.Status.Conditions, metav1.Condition{
				Type:    typeUnknownWebpage,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			})
		if err = r.Status().Update(ctx, webpage); err != nil {
			log.Error(err, "Failed to update webpage status")
			return ctrl.Result{}, err
		}
		// Re-fetch the webpage Custom Resource before update the status
		// so that we have the latest state of the resource on the cluster, and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
			log.Error(err, "Failed to re-fetch webpage")
			return ctrl.Result{}, err
		}
	}

	// Check if the deployment already exists, if not create a new one
	founddep := &appsv1.Deployment{}

	// Re-fetch the webpage Custom Resource before update the status
	// so that we have the latest state of the resource on the cluster, and we will avoid
	// raise the issue "the object has been modified, please apply
	// your changes to the latest version and try again" which would re-trigger the reconciliation
	if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
		log.Error(err, "Failed to re-fetch webpage")
		return ctrl.Result{}, err
	}

	err = r.Get(ctx, types.NamespacedName{Name: webpage.Name, Namespace: webpage.Namespace}, founddep)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		dep, err := r.deploymentForWebpage(webpage)
		if err != nil {
			log.Error(err, "Failed to define new Deployment resource for Webpage")

			// The following implementation will update the status
			meta.SetStatusCondition(
				&webpage.Status.Conditions,
				metav1.Condition{
					Type:   typeErrorWebpage,
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf(
						"Failed to create Deployment for the custom resource (%s): (%s)", webpage.Name, err),
				})

			if err := r.Status().Update(ctx, webpage); err != nil {
				log.Error(err, "Failed to update Webpage status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

		// Re-fetch the webpage Custom Resource before update the status
		// so that we have the latest state of the resource on the cluster, and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
			log.Error(err, "Failed to re-fetch webpage")
			return ctrl.Result{}, err
		}

		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// Update Deployment
	dep, err := r.deploymentForWebpage(webpage)
	if err != nil {
		log.Error(err, "Failed to define new Deployment resource for Webpage")

		// The following implementation will update the status
		meta.SetStatusCondition(
			&webpage.Status.Conditions,
			metav1.Condition{
				Type:   typeErrorWebpage,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf(
					"Failed to create Deployment for the custom resource (%s): (%s)", webpage.Name, err),
			})

		if err := r.Status().Update(ctx, webpage); err != nil {
			log.Error(err, "Failed to update Webpage status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}
	if depUpdate(founddep, dep) {

		log.Info("Update Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

		// Re-fetch the webpage Custom Resource before update the status
		// so that we have the latest state of the resource on the cluster, and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
			log.Error(err, "Failed to re-fetch webpage")
			return ctrl.Result{}, err
		}

		if err = r.Update(ctx, dep); err != nil {
			log.Error(err, "Failed to Update Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	foundsvc := &corev1.Service{}

	// Re-fetch the webpage Custom Resource before update the status
	// so that we have the latest state of the resource on the cluster, and we will avoid
	// raise the issue "the object has been modified, please apply
	// your changes to the latest version and try again" which would re-trigger the reconciliation
	if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
		log.Error(err, "Failed to re-fetch webpage")
		return ctrl.Result{}, err
	}
	err = r.Get(ctx, types.NamespacedName{Name: webpage.Name, Namespace: webpage.Namespace}, foundsvc)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new Service
		svc, err := r.serviceForWebpage(webpage)
		if err != nil {
			log.Error(err, "Failed to define new Service resource for Webpage")

			// The following implementation will update the status
			meta.SetStatusCondition(
				&webpage.Status.Conditions,
				metav1.Condition{
					Type:   typeErrorWebpage,
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf(
						"Failed to create Service for the custom resource (%s): (%s)", webpage.Name, err),
				})

			if err := r.Status().Update(ctx, webpage); err != nil {
				log.Error(err, "Failed to update Webpage status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Service",
			"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)

		// Re-fetch the webpage Custom Resource before update the status
		// so that we have the latest state of the resource on the cluster, and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
			log.Error(err, "Failed to re-fetch webpage")
			return ctrl.Result{}, err
		}

		if err = r.Create(ctx, svc); err != nil {
			log.Error(err, "Failed to create new Service",
				"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}

		// Service created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	foundingress := &netv1.Ingress{}

	// Re-fetch the webpage Custom Resource before update the status
	// so that we have the latest state of the resource on the cluster, and we will avoid
	// raise the issue "the object has been modified, please apply
	// your changes to the latest version and try again" which would re-trigger the reconciliation
	if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
		log.Error(err, "Failed to re-fetch webpage")
		return ctrl.Result{}, err
	}
	err = r.Get(ctx, types.NamespacedName{Name: webpage.Name, Namespace: webpage.Namespace}, foundingress)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new Service
		ing, err := r.ingressForWebpage(webpage)
		if err != nil {
			log.Error(err, "Failed to define new Ingress resource for Webpage")

			// The following implementation will update the status
			meta.SetStatusCondition(
				&webpage.Status.Conditions,
				metav1.Condition{
					Type:   typeErrorWebpage,
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf(
						"Failed to create Ingress for the custom resource (%s): (%s)", webpage.Name, err),
				})

			if err := r.Status().Update(ctx, webpage); err != nil {
				log.Error(err, "Failed to update Webpage status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Ingress",
			"Ingress.Namespace", ing.Namespace, "Ingress.Name", ing.Name)

		// Re-fetch the webpage Custom Resource before update the status
		// so that we have the latest state of the resource on the cluster, and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
			log.Error(err, "Failed to re-fetch webpage")
			return ctrl.Result{}, err
		}

		if err = r.Create(ctx, ing); err != nil {
			log.Error(err, "Failed to create new Ingress",
				"Ingress.Namespace", ing.Namespace, "Ingress.Name", ing.Name)
			return ctrl.Result{}, err
		}

		// Ingress created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Ingress")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// Update Ingress
	ing, err := r.ingressForWebpage(webpage)
	if err != nil {
		log.Error(err, "Failed to define new Ingress resource for Webpage")

		// The following implementation will update the status
		meta.SetStatusCondition(
			&webpage.Status.Conditions,
			metav1.Condition{
				Type:   typeErrorWebpage,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf(
					"Failed to create Ingress for the custom resource (%s): (%s)", webpage.Name, err),
			})

		if err := r.Status().Update(ctx, webpage); err != nil {
			log.Error(err, "Failed to update Webpage status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}
	if ingUpdate(ing, foundingress, webpage) {

		log.Info("Update Ingress",
			"Ingress.Namespace", dep.Namespace, "Ingress.Name", dep.Name)

		// Re-fetch the webpage Custom Resource before update the status
		// so that we have the latest state of the resource on the cluster, and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
			log.Error(err, "Failed to re-fetch webpage")
			return ctrl.Result{}, err
		}

		if err = r.Update(ctx, ing); err != nil {
			log.Error(err, "Failed to Update Ingress",
				"Ingress.Namespace", dep.Namespace, "Ingress.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Ingress created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	deperr := r.Get(ctx, types.NamespacedName{Name: webpage.Name, Namespace: webpage.Namespace}, founddep)
	svcerr := r.Get(ctx, types.NamespacedName{Name: webpage.Name, Namespace: webpage.Namespace}, foundsvc)
	ingerr := r.Get(ctx, types.NamespacedName{Name: webpage.Name, Namespace: webpage.Namespace}, foundingress)

	if deperr == nil && svcerr == nil && ingerr == nil {
		meta.SetStatusCondition(
			&webpage.Status.Conditions,
			metav1.Condition{
				Type:   typeAvailableWebpage,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf(
					"Deployment, Service, Ingress are available for (%s)", webpage.Name),
			})

		if err := r.Status().Update(ctx, webpage); err != nil {
			log.Error(err, "Failed to update Webpage status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
// For(&cachev1alpha1.Webpage{}) specifies the Webpage type as the primary resource to watch.
// For each Webpage type Add/Update/Delete event the reconcile loop will be sent a reconcile Request
// (a namespace/name key) for that Webpage object.
func (r *StaticReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webpagev1alpha1.Static{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&netv1.Ingress{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}

// deploymentForWebpage returns a Webpage Deployment object
func (r *StaticReconciler) deploymentForWebpage(webpage *webpagev1alpha1.Static) (*appsv1.Deployment, error) {
	ls := labelsForWebpage(webpage.Name, webpage)
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webpage.Name,
			Namespace: webpage.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:           constNginxImage,
						Name:            "nginx",
						ImagePullPolicy: corev1.PullAlways,
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
							Name:          "webpage",
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "webpage",
							ReadOnly:  true,
							MountPath: nginxServeFolder,
						}},
					},
						{
							Image:           constGitsyncImage,
							Name:            "git-sync",
							ImagePullPolicy: corev1.PullAlways,
							Args: []string{
								"--repo=" + webpage.Spec.Repository,
								"--branch=" + webpage.Spec.Branch,
								"--wait=" + strconv.Itoa(webpage.Spec.Wait),
								"--root=" + nginxServeFolder,
								"--dest=" + nginxServeFolderLink,
								"--git-config=safe.directory:/usr/share/nginx",
								"--submodules=off",
								"--v=2",
							},
							VolumeMounts: []corev1.VolumeMount{{
								Name:      "webpage",
								ReadOnly:  false,
								MountPath: nginxServeFolder,
							}},
						},
					},
					Volumes: []corev1.Volume{{
						Name: "webpage",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: nil,
						}},
					},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(webpage, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

func (r *StaticReconciler) serviceForWebpage(webpage *webpagev1alpha1.Static) (*corev1.Service, error) {
	ls := labelsForWebpage(webpage.Name, webpage)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webpage.Name,
			Namespace: webpage.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name:     "webpage",
				Protocol: "TCP",
				Port:     80,
			}},
			Selector: ls,
			Type:     "ClusterIP",
		},
	}

	// Set the ownerRef for the Service
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(webpage, svc, r.Scheme); err != nil {
		return nil, err
	}
	return svc, nil
}

func (r *StaticReconciler) ingressForWebpage(webpage *webpagev1alpha1.Static) (*netv1.Ingress, error) {
	ls := labelsForWebpage(webpage.Name, webpage)
	pt := netv1.PathType(netv1.PathTypePrefix)

	tls := make([]netv1.IngressTLS, len(webpage.Spec.Ingress.Host))
	if webpage.Spec.Ingress.Tls {
		for k, v := range webpage.Spec.Ingress.Host {
			tls[k] = netv1.IngressTLS{
				Hosts: []string{
					v,
				},
				SecretName: webpage.Name + "tls-secret-" + strings.Replace(v, ".", "-", -1),
			}
		}
	} else {
		tls = make([]netv1.IngressTLS, 0)
	}

	rule := make([]netv1.IngressRule, len(webpage.Spec.Ingress.Host))
	for k, v := range webpage.Spec.Ingress.Host {
		rule[k] = netv1.IngressRule{
			Host: v,
			IngressRuleValue: netv1.IngressRuleValue{
				HTTP: &netv1.HTTPIngressRuleValue{
					Paths: []netv1.HTTPIngressPath{{
						Path:     "/",
						PathType: &pt,
						Backend: netv1.IngressBackend{
							Service: &netv1.IngressServiceBackend{
								Name: webpage.Name,
								Port: netv1.ServiceBackendPort{
									Name: "webpage",
								},
							},
						},
					}},
				}},
		}
	}

	ingress := &netv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        webpage.Name,
			Namespace:   webpage.Namespace,
			Annotations: webpage.Spec.Ingress.Annotations,
			Labels:      ls,
		},
		Spec: netv1.IngressSpec{
			Rules: rule,
			TLS:   tls,
		},
	}

	// Set the ownerRef for the Service
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(webpage, ingress, r.Scheme); err != nil {
		return nil, err
	}
	return ingress, nil
}

func labelsForWebpage(name string, webpage *webpagev1alpha1.Static) map[string]string {
	image := constNginxImage
	imageTag := strings.Split(image, ":")[1]

	return map[string]string{
		"app.kubernetes.io/name":          "Webpage",
		"app.kubernetes.io/instance":      name,
		"app.kubernetes.io/nginx-version": imageTag,
		"app.kubernetes.io/part-of":       "static-webpage-operator",
		"app.kubernetes.io/created-by":    "controller-manager",
	}
}

// Here we check if Ingress is need to be updated
func ingUpdate(ing *netv1.Ingress, foundingress *netv1.Ingress, webpage *webpagev1alpha1.Static) bool {
	// if number of hosts is different - do update
	if len(foundingress.Spec.Rules) != len(ing.Spec.Rules) {
		return true
	}
	// if number of hosts is the same we check if they are different - do update
	for k, v := range foundingress.Spec.Rules {
		if v.Host != ing.Spec.Rules[k].Host {
			return true
		}
	}
	// if number of ingress tls hosts is more then 0, but tls is false in CR manifest - do update
	// OR if number of ingress tls hosts is 0, but tls is true in CR manifest - do update
	if len(ing.Spec.TLS) > 0 && !webpage.Spec.Ingress.Tls {
		return true
	} else if len(foundingress.Spec.TLS) == 0 && webpage.Spec.Ingress.Tls {
		return true
	}
	// if number of annotations in Current Ingress is different then in New Ingress - do update
	if len(foundingress.ObjectMeta.Annotations) != len(ing.ObjectMeta.Annotations) {
		return true
	}
	for k, v := range foundingress.ObjectMeta.Annotations {
		// if we canNOT find Key in Current Ingress, which is exists in New Ingress - do update
		// if Value of found Key in Current Ingress is different to Value in New Ingress - do update
		if ing.ObjectMeta.Annotations[k] == "" {
			return true
		} else if ing.ObjectMeta.Annotations[k] != v {
			return true
		}
	}

	return false
}

// Here we check if Deployment is need to be updated
func depUpdate(founddep *appsv1.Deployment, dep *appsv1.Deployment) bool {
	// Check:
	// - Repo - Containers[1].Args[0]
	// - Branch - Containers[1].Args[1]
	// - Wait - Containers[1].Args[2]
	if founddep.Spec.Template.Spec.Containers[1].Args[0] != dep.Spec.Template.Spec.Containers[1].Args[0] ||
		founddep.Spec.Template.Spec.Containers[1].Args[1] != dep.Spec.Template.Spec.Containers[1].Args[1] ||
		founddep.Spec.Template.Spec.Containers[1].Args[2] != dep.Spec.Template.Spec.Containers[1].Args[2] {
		return true
	}
	return false
}
