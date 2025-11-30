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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1alpha1 "pwlctk.ovh/kroc/api/v1alpha1"
)

var _ = Describe("Kroc Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		kroc := &appsv1alpha1.Kroc{}
		ResourceToCreate := `
    {{- $pod1 := 3 -}}
    {{- $pod2 := 4 -}}
    apiVersion: v1
    kind: Pod
    metadata:
      name: nginx-pod-{{ $pod1 }}
      namespace: pawel
      labels:
        environment: development
    spec:
      containers:
      - name: nginx-container
        image: nginx:latest
        ports:
        - containerPort: 80
          protocol: TCP
    ---
    apiVersion: v1
    kind: Pod
    metadata:
      name: nginx-pod-{{ $pod2 }}
      namespace: pawel
      labels:
        environment: development
    spec:
      containers:
      - name: nginx-container
        image: nginx:latest
        ports:
        - containerPort: 80
          protocol: TCP
`
		var k8sManager ctrl.Manager
		var cancelManager context.CancelFunc

		BeforeEach(func() {

			// Step 1: INITIALIZE THE TEST MANAGER
			var err error
			k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
				Scheme: k8sClient.Scheme(),
			})
			Expect(err).NotTo(HaveOccurred())

			// Step 2: START THE MANAGER IN A GOROUTINE
			// The manager needs to run to handle events and watches.
			var mgrCtx context.Context
			mgrCtx, cancelManager = context.WithCancel(ctx)
			go func() {
				defer GinkgoRecover()
				Expect(k8sManager.Start(mgrCtx)).To(Succeed(), "failed to run manager")
			}()

			// Give the manager a brief moment to start its caches
			time.Sleep(100 * time.Millisecond)

			By("creating the custom resource for the Kind Kroc")
			err = k8sClient.Get(ctx, typeNamespacedName, kroc)
			if err != nil && errors.IsNotFound(err) {
				resource := &appsv1alpha1.Kroc{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: appsv1alpha1.KrocSpec{
						WatchObject: appsv1alpha1.WatchObjectSpec{
							ApiVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "pawel",
							Namespace:  "pawel",
						},
						ResourceToCreate: &ResourceToCreate,
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// Step 3: STOP THE MANAGER
			if cancelManager != nil {
				cancelManager()
			}

			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &appsv1alpha1.Kroc{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Kroc")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &KrocReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				watchDB:  map[types.UID]*KrocControllerDb{},
				createDB: map[types.UID]*createControllerDb{},
			}
			Expect(controllerReconciler.SetupWithManager(k8sManager)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
