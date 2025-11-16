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
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	appsv1alpha1 "pwlctk.ovh/kroc/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type watchReconciler struct {
	client.Client
	watchedObj           *unstructured.Unstructured
	resourceToCreateTmpl *string
	krocObjectDetails    string
	mgr                  ctrl.Manager
	createDB             map[types.UID]*createControllerDb
	createDBMutex        *sync.RWMutex
	watchContext         *context.Context
	setKrocStatus        StatusSetter
	krocObj              *appsv1alpha1.Kroc
	krocObjectUID        types.UID
}

func (r *watchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	if err := r.Get(ctx, req.NamespacedName, r.watchedObj); err != nil {
		fmt.Println(err.Error())
		// Handle "Not Found" as a definitive deletion
		if errors.IsNotFound(err) {
			// Pod is gone from the API (deletion completed)
			klog.InfoS(err.Error())
			return ctrl.Result{}, nil
		}
		// Handle other errors (network issues, etc.)
		return ctrl.Result{}, err

	}

	tmpl, err := template.New("Template").Parse(*r.resourceToCreateTmpl)

	if err != nil {
		klog.InfoS("Error parsing Template", "KrocName", r.krocObj.GetName(), "KrocNamespace", r.krocObj.GetNamespace())
		if err := r.setKrocStatus(&ctx, r.krocObj, "Parsing Error"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	var buffer bytes.Buffer

	var parent map[string]interface{}
	parent = make(map[string]interface{})
	parent["parent"] = r.watchedObj.Object

	err = tmpl.Execute(&buffer, parent)
	if err != nil {
		klog.InfoS("Error executing Template", "Name", r.watchedObj.GetName(), "Namespace", r.watchedObj.GetNamespace())
		if err := r.setKrocStatus(&ctx, r.krocObj, "Parsing Error"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	outputString := buffer.String()

	objects, err := processMultiManifests([]byte(outputString))
	if err != nil {
		klog.ErrorS(err, "error")
		if err := r.setKrocStatus(&ctx, r.krocObj, "Parsing Error"); err != nil {
			return ctrl.Result{}, err
		}
	}

	klog.InfoS("Successfully processed objects", "Number", len(objects), "Name", r.watchedObj.GetName(), "Namespace", r.watchedObj.GetNamespace())

	watchedObjUID := r.watchedObj.GetUID() + r.krocObjectUID

	for _, obj := range objects {

		const myAnnotationKey = "kroc.pwlctk.ovh/last-template-MD5"
		hash, err := CalculateMD5Checksum(obj)

		if err != nil {
			fmt.Printf("Error calculating MD5: %v\n", err)
			break
		}

		err = r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)
		objBeforeUpdate := obj.DeepCopy()

		annotations := obj.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}

		if errors.IsNotFound(err) {
			klog.InfoS("Attempting to create resource",
				"APIVersion", obj.GetAPIVersion(),
				"Kind", obj.GetKind(),
				"Name", obj.GetName(),
			)
			if err := controllerutil.SetControllerReference(r.watchedObj, obj, r.Client.Scheme()); err != nil {
				klog.ErrorS(err, "Couldn't set Controller Reference")
			}

			annotations[myAnnotationKey] = hash
			obj.SetAnnotations(annotations)

			if err := r.Client.Create(ctx, obj); err != nil {
				if errors.IsAlreadyExists(err) {
					// If the Pod already exists (safe to ignore)
					klog.InfoS("Resource already exists, skipping creation")
					return ctrl.Result{}, nil
				}
				// Handle all other errors
				klog.Error(err, "Failed to create Pod")
				return ctrl.Result{}, err
			}

			klog.InfoS("Resource created successfully", "Name", obj.GetName(), "Namespace", obj.GetNamespace(), "Kind", obj.GetKind())
		}

		if existingValue, ok := annotations[myAnnotationKey]; ok {
			if existingValue != hash {
				klog.Info("Annotation value is different than desired - removing the object",
					"key", myAnnotationKey,
					"current", existingValue,
					"desired", hash,
				)
				objToRemove := []*unstructured.Unstructured{
					obj,
				}

				if obj.GetDeletionTimestamp() != nil {
					klog.InfoS("Resource is being removed...", "Kind", obj.GetKind(), "Name", obj.GetName(), "Namespace", obj.GetNamespace())
					return ctrl.Result{Requeue: true}, nil
				}

				if err := deleteResources(ctx, r.Client, objToRemove); err != nil {
					klog.Error(err, "Failed to delete dependent resources")
					return ctrl.Result{}, err // Return the error to trigger requeue
				}

				r.createDBMutex.Lock()
				ctrl_config, createControllerExists := r.createDB[watchedObjUID]
				r.createDBMutex.Unlock()
				if createControllerExists {
					(*ctrl_config.ctxCancel)()
					ctrl_config.wg.Wait()

					// r.createDBMutex.Lock()
					// delete(r.createDB, watchedObjUID)
					// r.createDBMutex.Unlock()
				}
				return ctrl.Result{Requeue: true}, nil
			}
		} else {

			annotations[myAnnotationKey] = hash
			obj.SetAnnotations(annotations)

			if err := r.Client.Patch(ctx, obj, client.MergeFrom(objBeforeUpdate)); err != nil {
				klog.Error(err, "Failed to patch annotation on existing resource")
				return ctrl.Result{}, err
			}
			klog.InfoS("Annotation added to desired object", "key", myAnnotationKey, "value", hash)
			return ctrl.Result{Requeue: true}, nil
		}
	}

	createControllerName := fmt.Sprintf("create-controller-for-%s", r.krocObjectDetails)
	var CreateCtx context.Context
	var CreateCtxCancel context.CancelFunc

	// Check if create controller is already registered
	r.createDBMutex.Lock()
	_, createControllerExists := r.createDB[watchedObjUID]
	r.createDBMutex.Unlock()
	if createControllerExists {
		klog.InfoS("The Create controller has been registered already", "ControlerName", createControllerName)
		return ctrl.Result{}, nil
	}

	klog.InfoS("Registering the Create controller to DB", "ControllerName", createControllerName)

	CreateCtx, CreateCtxCancel = context.WithCancel(*r.watchContext)
	var wg sync.WaitGroup
	wg.Add(1)

	r.createDBMutex.Lock()
	(r.createDB)[watchedObjUID] = &createControllerDb{
		name: createControllerName,
		ctx:  &CreateCtx, ctxCancel: &CreateCtxCancel, wg: &wg}
	r.createDBMutex.Unlock()

	skip_valid := true
	createController, err := controller.NewUnmanaged(createControllerName, controller.Options{
		Reconciler: &createReconciler{
			Client:            r.Client,
			resourcesToCreate: objects,
			watchedObj:        r.watchedObj,
			setKrocStatus:     r.setKrocStatus,
			krocObj:           r.krocObj,
		},
		SkipNameValidation: &skip_valid,
	})

	if err != nil {
		klog.Info("failed to get new unmanaged controller for create", "controller", createControllerName)
		if err := r.setKrocStatus(&ctx, r.krocObj, "Failed"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	hdler := handler.TypedEnqueueRequestForOwner[client.Object](r.mgr.GetScheme(), r.mgr.GetRESTMapper(), r.watchedObj, handler.OnlyControllerOwner())

	for _, obj := range objects {
		klog.InfoS("Adding a resource to watch", "controller", createControllerName, "UID", watchedObjUID, "Kind", obj.GetKind(), "Name", obj.GetName(), "Namespace", obj.GetNamespace())

		targetGVK := obj.GroupVersionKind()
		ownedPlaceholder := &unstructured.Unstructured{}
		ownedPlaceholder.SetGroupVersionKind(targetGVK)
		createController.Watch(source.Kind[client.Object](r.mgr.GetCache(), ownedPlaceholder, hdler))
	}

	go func() {
		klog.InfoS("Starting the Create Controller", "controller", createControllerName, "UID", watchedObjUID)

		if err := createController.Start(CreateCtx); err != nil {
			klog.Error(err, "Failed to start status controller for gvk ")
			if err := r.setKrocStatus(&ctx, r.krocObj, "Failed"); err != nil {
				klog.ErrorS(err, "Create Controller goroutine")
			}
		}

		klog.InfoS("Stopping the Create Controller", "controller", createControllerName, "UID", watchedObjUID)

		r.createDBMutex.Lock()
		defer r.createDBMutex.Unlock()

		_, createControllerExists := r.createDB[watchedObjUID]
		if createControllerExists {
			delete(r.createDB, watchedObjUID)
		}

		wg.Done()
	}()

	return ctrl.Result{}, nil
}

func processMultiManifests(data []byte) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

	for {
		var u unstructured.Unstructured
		err := decoder.Decode(&u)

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("YAML syntax error during decoding: %w", err)
		}

		if u.Object == nil {
			continue
		}

		if u.GetAPIVersion() == "" || u.GetKind() == "" || u.GetName() == "" {
			return nil, fmt.Errorf("manifest is missing required fields (apiVersion, kind, or name)")
		}

		objects = append(objects, &u)
	}

	return objects, nil
}

func deleteResources(ctx context.Context, r client.Client, resources []*unstructured.Unstructured) error {

	for _, obj := range resources {
		klog.InfoS("Attempting to delete", "Kind", obj.GetKind(), "Namespace", obj.GetNamespace(), "Name", obj.GetName())

		deletePolicy := metav1.DeletePropagationBackground
		deleteOptions := client.DeleteOptions{
			PropagationPolicy: &deletePolicy,
		}

		var deleteErr error
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			deleteErr = r.Delete(ctx, obj.DeepCopy(), &deleteOptions)
			return deleteErr // Return the error to the RetryOnConflict loop
		})
		if err != nil {
			return err
		}

		finalErr := client.IgnoreNotFound(deleteErr)

		if finalErr != nil {
			return fmt.Errorf("failed to delete %s %s/%s: %w", obj.GetKind(), obj.GetNamespace(), obj.GetName(), finalErr)
		}

		if deleteErr != nil {
			klog.InfoS("The resource was already gone", "Kind", obj.GetKind(), "Name", obj.GetName(), "Namespace", obj.GetNamespace())
		} else {
			klog.InfoS("The resource deleted successfully", "Kind", obj.GetKind(), "Name", obj.GetName(), "Namespace", obj.GetNamespace())
		}
	}

	return nil
}

func CalculateMD5Checksum(obj *unstructured.Unstructured) (string, error) {
	canonicalBytes, err := json.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("failed to marshal object to JSON: %w", err)
	}
	hash := md5.Sum(canonicalBytes)
	return hex.EncodeToString(hash[:]), nil
}
