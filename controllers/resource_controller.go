/*
Copyright 2022.

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
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	resourcev1alpha1 "example.com/resourcemetadata/api/v1alpha1"
)

// ResourceReconciler reconciles a Resource object
type ResourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=resource.example.com,resources=resources,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=resource.example.com,resources=resources/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=resource.example.com,resources=resources/finalizers,verbs=update

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Resource object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// get the Resource resource
	var resource resourcev1alpha1.Resource
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		if errors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we  can get them
			// on deleted requests
			// return and don't requeue
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "unable to fetch resource")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	resourceOld := resource.DeepCopy()

	// new instance, so we mark it as pending
	if resource.Status.Status == "" {
		resource.Status.Status = resourcev1alpha1.StatusPending
	}

	switch resource.Status.Status {
	case resourcev1alpha1.StatusPending:
		resource.Status.Status = resourcev1alpha1.StatusRunning

		err := r.Status().Update(context.TODO(), &resource)
		if err != nil {
			log.Error(err, "failed to update resource status")
			return ctrl.Result{}, err
		} else {
			log.Info("updated resource status: " + resource.Status.Status)
			return ctrl.Result{Requeue: true}, nil
		}
	case resourcev1alpha1.StatusRunning:
		pod := createPod(&resource)
		query := &corev1.Pod{}

		err := r.Client.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: pod.ObjectMeta.Name}, query)
		if err != nil && errors.IsNotFound(err) {
			log.Info("pod not found, creating")

			err = ctrl.SetControllerReference(&resource, pod, r.Scheme)
			if err != nil {
				return ctrl.Result{}, err
			}

			err = r.Create(ctx, pod)
			if err != nil {
				log.Error(err, "error creating pod")
				return ctrl.Result{}, err
			}

			log.Info("pod created successfully", "name", pod.Name)
			return ctrl.Result{}, nil
		} else if err != nil {
			log.Error(err, "cannot get pod")
			return ctrl.Result{}, err
		} else if query.Status.Phase == corev1.PodFailed || query.Status.Phase == corev1.PodSucceeded {
			log.Info("container terminated", "reason", query.Status.Reason, "message", query.Status.Message)
			resource.Status.Status = resourcev1alpha1.StatusCleaning
		} else if query.Status.Phase == corev1.PodRunning {
			return ctrl.Result{Requeue: true}, nil
		} else if query.Status.Phase == corev1.PodPending {
			return ctrl.Result{Requeue: true}, nil
		} else {
			return ctrl.Result{}, nil
		}

		if !reflect.DeepEqual(resourceOld.Status, resource.Status) {
			err = r.Status().Update(ctx, &resource)
			if err != nil {
				log.Error(err, "failed to update client status from running")
				return ctrl.Result{}, err
			} else {
				log.Info("updated resource status RUNNING -> " + resource.Status.Status)
				return ctrl.Result{Requeue: true}, nil
			}
		}
	case resourcev1alpha1.StatusCleaning:
		pod := createPod(&resource)
		query := &corev1.Pod{}

		err := r.Client.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: pod.ObjectMeta.Name}, query)
		if err == nil && resource.ObjectMeta.DeletionTimestamp.IsZero() {
			err = r.Delete(ctx, query)
			if err != nil {
				log.Error(err, "failed to remove old pod")
				return ctrl.Result{}, err
			} else {
				log.Info("old pod removed")
				return ctrl.Result{Requeue: true}, nil
			}
		}

		resource.Status.Status = resourcev1alpha1.StatusPending
		if !reflect.DeepEqual(resourceOld.Status, resource.Status) {
			err = r.Status().Update(context.TODO(), &resource)
			if err != nil {
				log.Error(err, "failed to update resource status")
				return ctrl.Result{}, err
			} else {
				log.Info("updated resource status CLEANING -> " + resource.Status.Status)
				return ctrl.Result{Requeue: true}, nil
			}
		}
	default:
	}

	return ctrl.Result{}, nil
}

// createPod creates an object of the type Pod, populating the required fields with information from the CR
func createPod(resource *resourcev1alpha1.Resource) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: resource.Spec.Template.ObjectMeta,
		// ObjectMeta: metav1.ObjectMeta{
		// 	Name:      "resourcepod",
		// 	Namespace: "default",
		// 	Labels: map[string]string{
		// 		"app":     "someLable",
		// 		"version": "v1alpha1",
		// 	},
		// },
		Spec: resource.Spec.Template.Spec,
	}
}

var (
	podOwnerKey = ".metadata.controller"
	apiGVstr    = resourcev1alpha1.GroupVersion.String()
)

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, podOwnerKey, func(o client.Object) []string {
		// grab the pod object, extract the owner
		pod := o.(*corev1.Pod)
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			return nil
		}
		// make sure it's a Resource
		if owner.APIVersion != apiGVstr || owner.Kind != "Resource" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcev1alpha1.Resource{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
