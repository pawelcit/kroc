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
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	appsv1alpha1 "pwlctk.ovh/kroc/api/v1alpha1"
)

type KrocControllerDb struct {
	name      string
	ctx       *context.Context
	ctxCancel *context.CancelFunc
	specMD5   string
	wg        *sync.WaitGroup
	// events chan client.Object
}

// KrocReconciler reconciles a Kroc object
type KrocReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	mgr          ctrl.Manager
	watchDB      map[types.UID]*KrocControllerDb
	watchDBMutex sync.RWMutex

	createDB      map[types.UID]*createControllerDb
	createDBMutex sync.RWMutex
}

type StatusSetter func(ctx *context.Context, obj *appsv1alpha1.Kroc, state string) error

// +kubebuilder:rbac:groups=apps.pwlctk.ovh,resources=krocs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.pwlctk.ovh,resources=krocs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.pwlctk.ovh,resources=krocs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Kroc object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/reconcile
func (r *KrocReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	krocObject := &appsv1alpha1.Kroc{}
	if err := r.Get(ctx, req.NamespacedName, krocObject); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	parts := strings.Split(krocObject.Spec.WatchObject.ApiVersion, "/")
	group := parts[0]
	version := parts[1]
	kind := krocObject.Spec.WatchObject.Kind

	gvkWatchedObject := schema.GroupVersionKind{
		Group:   group,
		Version: version,
		Kind:    kind,
	}

	krocObjectDetails := fmt.Sprintf("%s/%s/%s-%s-ns-%s", gvkWatchedObject.Group, gvkWatchedObject.Version, gvkWatchedObject.Kind, krocObject.Name, krocObject.Namespace)
	watchControllerName := fmt.Sprintf("watch-controller-for-%s", krocObjectDetails)
	krocObjectUID := krocObject.GetUID()

	const krocFinalizer = "finalizer.kroc.pwlctk.ovh/kroc"

	if krocObject.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(krocObject, krocFinalizer) {

			klog.InfoS("Deleting Kroc resource",
				"kroc", krocObject.Name,
				"namespace", krocObject.Namespace)
			// Perform any additional cleanup logic if necessary
			// Remove finalizer
			controllerutil.RemoveFinalizer(krocObject, krocFinalizer)
			if err := r.Update(ctx, krocObject); err != nil {
				return ctrl.Result{}, err
			}

			// Check if Db has controlelr name
			r.watchDBMutex.Lock()
			ctrl_config, ctrl_name := r.watchDB[krocObjectUID]
			r.watchDBMutex.Unlock()
			if ctrl_name {
				klog.InfoS("Controller Canceling", "name", ctrl_config.name)
				(*ctrl_config.ctxCancel)()
			}
		}
		return ctrl.Result{}, nil
	}

	// Add a finalizer if it doesn't already have one
	if !controllerutil.ContainsFinalizer(krocObject, krocFinalizer) {
		controllerutil.AddFinalizer(krocObject, krocFinalizer)
		if err := r.Update(ctx, krocObject); err != nil {
			return ctrl.Result{}, err
		}
	}

	watchedObj := &unstructured.Unstructured{}
	watchedObj.SetGroupVersionKind(gvkWatchedObject)

	var WatchCtx context.Context
	var WatchCtxCancel context.CancelFunc

	resourceToCreateTmpl := *krocObject.Spec.ResourceToCreate

	specBytes, _ := json.Marshal(krocObject.Spec)
	specMD5 := calculateMD5(specBytes)

	r.watchDBMutex.Lock()
	ctrl_config, krocControllerExists := r.watchDB[krocObjectUID]
	r.watchDBMutex.Unlock()

	if krocControllerExists {
		klog.InfoS("The Watch controller has been registered already", "ControlerName", watchControllerName)
		// checking if md5 has changed
		if r.watchDB[krocObjectUID].specMD5 != specMD5 {
			klog.Info("MD5 of SPEC different, stopping the Watch controller", "ControllerName", watchControllerName)
			(*ctrl_config.ctxCancel)()

			ctrl_config.wg.Wait()
			// r.watchDBMutex.Lock()
			// delete(r.watchDB, krocObjectUID)
			// r.watchDBMutex.Unlock()
		} else {
			return ctrl.Result{}, nil
		}
	}

	klog.InfoS("Registering the Watch Controller to DB", "ControllerName", watchControllerName)
	WatchCtx, WatchCtxCancel = context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	r.watchDBMutex.Lock()
	r.watchDB[krocObjectUID] = &KrocControllerDb{name: watchControllerName, ctx: &WatchCtx, ctxCancel: &WatchCtxCancel, specMD5: specMD5, wg: &wg}
	r.watchDBMutex.Unlock()

	skip_valid := true
	watchController, err := controller.NewUnmanaged(watchControllerName, controller.Options{
		Reconciler: &watchReconciler{
			Client:               r.Client,
			watchedObj:           watchedObj,
			resourceToCreateTmpl: &resourceToCreateTmpl,
			krocObjectDetails:    krocObjectDetails,
			mgr:                  r.mgr,
			createDB:             r.createDB,
			createDBMutex:        &r.createDBMutex,
			watchContext:         &WatchCtx,
			setKrocStatus:        r.SetStatus,
			krocObj:              krocObject,
			krocObjectUID:        krocObjectUID,
		},
		SkipNameValidation: &skip_valid,
	})

	if err != nil {
		klog.Info("Failed to get new unmanaged controller",
			"kind", gvkWatchedObject.Kind,
			"error", err)
		return ctrl.Result{}, err
	}

	cache := r.mgr.GetCache()
	informer, err := cache.GetInformer(ctx, watchedObj)
	if err != nil {
		klog.ErrorS(err, "failed to get informer for")
	}

	if !informer.HasSynced() {
		if !cache.WaitForCacheSync(ctx) {
			klog.ErrorS(err, "Failed waiting for informer")
		}
	}

	if err := watchController.Watch(source.Kind[client.Object](r.mgr.GetCache(), watchedObj, &handler.TypedEnqueueRequestForObject[client.Object]{},
		NewTargetUnstructuredPredicate(
			krocObject.Spec.WatchObject.Name,
			krocObject.Spec.WatchObject.Namespace,
			r.createDB,
			&r.createDBMutex,
			krocObjectUID))); err != nil {
		// prawdopodobnie log do poprawy
		klog.Info("Failed to add delete event watch to controller for gvk",
			"kind", gvkWatchedObject.Kind,
			"error", err)
	}

	r.SetStatus(&ctx, krocObject, "Initializing")

	go func() {
		klog.InfoS("Starting the Watch Controller", "controller", watchControllerName)

		if err := watchController.Start(WatchCtx); err != nil {
			klog.InfoS("Failed to start status controller for gvk",
				"kind", gvkWatchedObject.Kind,
				"error", err)
		}

		klog.InfoS("Stopping the Watch Controller", "controller", watchControllerName)

		r.watchDBMutex.Lock()
		defer r.watchDBMutex.Unlock()

		_, watchControllerExists := r.watchDB[krocObjectUID]
		if watchControllerExists {
			delete(r.watchDB, krocObjectUID)
		}

		wg.Done()
	}()

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KrocReconciler) SetupWithManager(mgr ctrl.Manager) error {

	r.mgr = mgr
	r.watchDB = map[types.UID]*KrocControllerDb{}
	r.createDB = map[types.UID]*createControllerDb{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.Kroc{}).
		Named("kroc-controller").
		Complete(r)
}

func (r *KrocReconciler) SetStatus(ctx *context.Context, obj *appsv1alpha1.Kroc, state string) error {

	obj.Status.Phase = state
	if err := r.Status().Update(*ctx, obj); err != nil {
		return err
	}
	return nil
}

func NewTargetUnstructuredPredicate(name string, namespace string,
	createDB map[types.UID]*createControllerDb, createDBMutex *sync.RWMutex,
	krocObjectUID types.UID) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return isTargetResource(e.Object, name, namespace)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isTargetResource(e.ObjectNew, name, namespace)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			fmt.Println("DELETE")
			watchedObjUID := e.Object.GetUID() + krocObjectUID
			createDBMutex.Lock()
			defer createDBMutex.Unlock()

			ctrlConfig, createControllerExists := (createDB)[watchedObjUID]

			if createControllerExists {
				(*ctrlConfig.ctxCancel)()
				delete(createDB, watchedObjUID)
			}

			return isTargetResource(e.Object, name, namespace)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			fmt.Println("GENERIC")
			return isTargetResource(e.Object, name, namespace)
		},
	}
}

func isTargetResource(obj client.Object, name string, namespace string) bool {
	namePattern := regexp.MustCompile(name)
	nameSpacePattern := regexp.MustCompile(namespace)
	return namePattern.MatchString(obj.GetName()) && nameSpacePattern.MatchString(obj.GetNamespace())
}

func calculateMD5(input []byte) string {
	hashArray := md5.Sum(input)
	return hex.EncodeToString(hashArray[:])
}
