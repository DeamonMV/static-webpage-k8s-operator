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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webpagev1alpha1 "github.com/DeamonMV/static-webpage-k8s-operator/api/v1alpha1"
)

const webpageFinalizer = "statics.webpage.daemon.io/finalizer"
const nginxServeFolder = "/usr/share/nginx/html"

// Definitions to manage status conditions
const (
	// typeAvailableWebpage represents the status of the Deployment reconciliation
	typeAvailableWebpage = "Available"
	// typeDegradedWebpage represents the status used when the custom resource is deleted and the finalizer operations are must to occur.
	typeDegradedWebpage  = "Degraded"
	typeDeployingWebpage = "Deploying"
	typeUnknownWebpage   = "Unknown"
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
//+kubebuilder:rbac:groups=webpage.daemon.io,resources=statics/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
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

	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(webpage, webpageFinalizer) {
		log.Info("Adding Finalizer for Webpage")

		if ok := controllerutil.AddFinalizer(webpage, webpageFinalizer); !ok {
			log.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, webpage); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Check if the Webpage instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isWebpageMarkedToBeDeleted := webpage.GetDeletionTimestamp() != nil
	if isWebpageMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(webpage, webpageFinalizer) {
			log.Info("Performing Finalizer Operations for Webpage before delete CR")

			// Re-fetch the webpage Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster, and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
				log.Error(err, "Failed to re-fetch webpage")
				return ctrl.Result{}, err
			}

			// Let's add here a status "Downgrade" to define that this resource begin its process to be terminated.
			meta.SetStatusCondition(
				&webpage.Status.Conditions,
				metav1.Condition{
					Type:    typeDegradedWebpage,
					Status:  metav1.ConditionUnknown,
					Reason:  "Finalizing",
					Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", webpage.Name),
				})

			if err := r.Status().Update(ctx, webpage); err != nil {
				log.Error(err, "Failed to update Webpage status")
				return ctrl.Result{}, err
			}
			// Perform all operations required before remove the finalizer and allow
			// the Kubernetes API to remove the custom resource.
			r.doFinalizerOperationsForWebpage(ctx, webpage)

			// The following implementation will raise an event

			// TODO(user): If you add operations to the doFinalizerOperationsForWebpage method
			// then you need to ensure that all worked fine before deleting and updating the Downgrade status
			// otherwise, you should requeue here.

			// Re-fetch the webpage Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster, and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, webpage); err != nil {
				log.Error(err, "Failed to re-fetch webpage")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(
				&webpage.Status.Conditions,
				metav1.Condition{
					Type:   typeDegradedWebpage,
					Status: metav1.ConditionTrue, Reason: "Finalizing",
					Message: fmt.Sprintf(
						"Finalizer operations for custom resource %s name were successfully accomplished", webpage.Name),
				})

			if err := r.Status().Update(ctx, webpage); err != nil {
				log.Error(err, "Failed to update webpage status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for webpage after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(webpage, webpageFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer for webpage")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, webpage); err != nil {
				log.Error(err, "Failed to remove finalizer for webpage")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
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
					Type:   typeAvailableWebpage,
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
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
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
					Type:   typeAvailableWebpage,
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

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
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
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}

// finalizeWebpage will perform the required operations before delete the CR.
func (r *StaticReconciler) doFinalizerOperationsForWebpage(ctx context.Context, cr *webpagev1alpha1.Static) {
	log := log.FromContext(ctx)
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.
	log.Info("Do some finalization")
	// Note: It is not recommended to use finalizers with the purpose of delete resources which are
	// created and managed in the reconciliation. These ones, such as the Deployment created on this reconcile,
	// are defined as depended of the custom resource. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the Deployment will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/
	//log.Info("Before Event message")
	// TODO https://github.com/DeamonMV/static-webpage-k8s-operator/issues/1
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s", cr.Name, cr.Namespace))
}

// deploymentForWebpage returns a Webpage Deployment object
func (r *StaticReconciler) deploymentForWebpage(webpage *webpagev1alpha1.Static) (*appsv1.Deployment, error) {
	ls := labelsForWebpage(webpage.Name, webpage)
	replicas := webpage.Spec.Size

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
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					//Affinity: &corev1.Affinity{
					//	NodeAffinity: &corev1.NodeAffinity{
					//		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				{
					//					MatchExpressions: []corev1.NodeSelectorRequirement{
					//						{
					//							Key:      "kubernetes.io/arch",
					//							Operator: "In",
					//							Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						},
					//						{
					//							Key:      "kubernetes.io/os",
					//							Operator: "In",
					//							Values:   []string{"linux"},
					//						},
					//					},
					//				},
					//			},
					//		},
					//	},
					//},
					// TODO implement flag to enable this, in case if container is capable to run nginx in non-root mode
					//SecurityContext: &corev1.PodSecurityContext{
					//	RunAsNonRoot: &[]bool{true}[0],
					//	// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
					//	// If you are looking for to produce solutions to be supported
					//	// on lower versions you must remove this option.
					//	SeccompProfile: &corev1.SeccompProfile{
					//		Type: corev1.SeccompProfileTypeRuntimeDefault,
					//	},
					//},
					Containers: []corev1.Container{{
						Image:           webpage.Spec.StaticSpecNginx.Image,
						Name:            "nginx",
						ImagePullPolicy: corev1.PullAlways,
						// TODO implement flag to enable this, in case if container is capable to run nginx in non-root mode
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						//SecurityContext: &corev1.SecurityContext{
						//	// WARNING: Ensure that the image used defines an UserID in the Dockerfile
						//	// otherwise the Pod will not run and will fail with "container has runAsNonRoot and image has non-numeric user"".
						//	// If you want your workloads admitted in namespaces enforced with the restricted mode in OpenShift/OKD vendors
						//	// then, you MUST ensure that the Dockerfile defines a User ID OR you MUST leave the "RunAsNonRoot" and
						//	// "RunAsUser" fields empty.
						//	RunAsNonRoot: &[]bool{true}[0],
						//	// The Webpage image does not use a non-zero numeric user as the default user.
						//	// Due to RunAsNonRoot field being set to true, we need to force the user in the
						//	// container to a non-zero numeric user. We do this using the RunAsUser field.
						//	// However, if you are looking to provide solution for K8s vendors like OpenShift
						//	// be aware that you cannot run under its restricted-v2 SCC if you set this value.
						//	RunAsUser:                &[]int64{1001}[0],
						//	AllowPrivilegeEscalation: &[]bool{false}[0],
						//	Capabilities: &corev1.Capabilities{
						//		Drop: []corev1.Capability{
						//			"ALL",
						//		},
						//	},
						//},
						Ports: []corev1.ContainerPort{{
							ContainerPort: webpage.Spec.ContainerPort,
							Name:          "webpage",
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "webpage",
							ReadOnly:  true,
							MountPath: nginxServeFolder,
						}},
					}},
					InitContainers: []corev1.Container{{
						Image:           webpage.Spec.StaticSpecGit.Image,
						Name:            "git",
						ImagePullPolicy: corev1.PullAlways,
						Args:            []string{"clone", "--single-branch", "-b" + webpage.Spec.StaticSpecGit.Branch, webpage.Spec.StaticSpecGit.Repository, nginxServeFolder},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "webpage",
							ReadOnly:  false,
							MountPath: nginxServeFolder,
						}},
					}},
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

func labelsForWebpage(name string, webpage *webpagev1alpha1.Static) map[string]string {
	image := webpage.Spec.StaticSpecNginx.Image
	imageTag := strings.Split(image, ":")[1]

	return map[string]string{
		"app.kubernetes.io/name":          "Webpage",
		"app.kubernetes.io/instance":      name,
		"app.kubernetes.io/nginx-version": imageTag,
		"app.kubernetes.io/part-of":       "static-webpage-operator",
		"app.kubernetes.io/created-by":    "controller-manager",
	}
}
