/*
Copyright 2025.

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

package controller

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	appsv1alpha1 "pwlctk.ovh/kroc/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type createControllerDb struct {
	name      string
	ctx       *context.Context
	ctxCancel *context.CancelFunc
	wg        *sync.WaitGroup
	// resourcesToCreateSpecMD5 string
	// objects                  []unstructured.Unstructured
}

type createReconciler struct {
	client.Client
	resourcesToCreate []*unstructured.Unstructured
	watchedObj        *unstructured.Unstructured
	setKrocStatus     StatusSetter
	krocObj           *appsv1alpha1.Kroc
}

func (r *createReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	for _, obj := range r.resourcesToCreate {

		obj.SetResourceVersion("")

		if err := controllerutil.SetControllerReference(r.watchedObj, obj, r.Client.Scheme()); err != nil {
			klog.ErrorS(err, "Couldn't set Controller Reference")
		}
		err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)

		if errors.IsNotFound(err) {
			klog.InfoS("Attempting to create resource",
				"APIVersion", obj.GetAPIVersion(),
				"Kind", obj.GetKind(),
				"Name", obj.GetName(),
			)
			if err := r.Client.Create(ctx, obj); err != nil {
				if errors.IsAlreadyExists(err) {
					// If the Pod already exists (safe to ignore)
					klog.InfoS("Pod already exists, skipping creation")
					return ctrl.Result{}, nil
				}
				// Handle all other errors
				klog.ErrorS(err, "Failed to create Pod")
				return ctrl.Result{}, err
			}

			klog.InfoS("Resource created successfully",
				"APIVersion", obj.GetAPIVersion(),
				"Kind", obj.GetKind(),
				"Name", obj.GetName())
		}

	}

	if err := r.setKrocStatus(&ctx, r.krocObj, "Ready"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil // Requeue to check status
}
