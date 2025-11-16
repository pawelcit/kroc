# 🚀 Kroc - Kubernetes Reactive Object Creator

This is an **educational hobby project** designed to demonstrate the creation of Kubernetes Operators using **Go (Golang)** and the **kubebuilder** framework.

The KROC operator introduces a custom resource, **`Kroc`**, which enables **reactive resource derivation**:
* It allows you to **watch** arbitrary Kubernetes objects (e.g., Deployments, Services) across the cluster.
* It **creates and manages** derived objects using attributes retrieved from the watched resources.

---

## ✨ Key Features & Behavior

* **Custom Resource Definition (CRD):** Defines the `Kroc` resource where all configuration (watched objects, templates) resides.
* **Go Templating:** Leverages the **Go Template Language** (`text/template`) within the `resourceToCreate` field, enabling dynamic and flexible generation of multiple derived Kubernetes objects.
* **Automatic Synchronization:**
    * If the **watched object changes**, all of its derived **child objects will be regenerated**.
    * If any **derived child object is manually removed**, the operator automatically **recreates** it to maintain the desired state.
* **Controller Composition:** Demonstrates the technique of **chaining three distinct controllers** to clearly separate the responsibilities of resource observation, custom resource management, and object generation.

---

## 🏗️ Architecture & Concept

The core architecture consists of three specialized and interconnected controllers: 

### 1. Kroc Controller (The Config Manager)

This is the primary controller that reconciles the custom **`Kroc`** resource. Its main role is to consume the configuration:
* **Monitors** all instances of the custom `Kroc` resource.
* **Initializes** and configures the `Watch Controller` based on the object specifications in the `Kroc` spec.
* **Manages** the overall lifecycle and status of the `Kroc` object.

### 2. Watch Controller (The Observer)

This controller handles the dynamic tracking of external resources defined in the `Kroc` configuration.

* **Sets up watches** on the arbitrary **target objects** specified by fields like `apiVersion`, `kind`, `nameRegex`, and `namespaceRegex`.
* When a target object changes, it **extracts** the required attributes (data to be used in the template) and **triggers** the `Creator Controller` to regenerate the children.

### 3. Creator Controller (The Derivation Handler)

This controller manages the creation and lifecycle of the final derived objects.

* **Applies** the resulting YAML/JSON to the cluster, handling creation logic to ensure the derived object's state matches the template.

---

## 💡 Example Usage

The `resourceToCreate` field uses the **Go Template Language**, allowing you to generate one or more Kubernetes objects dynamically. Data from the watched object is available via the `{{.parent}}` object (e.g., `{{.parent.metadata.uid}}`).

### Defining a Watch and Creating Multiple Pods

The following manifest creates a `Kroc` resource that watches Deployments matching the regex pattern "pawel" in namespaces matching "pawel". When a match is found, it uses templating to create **two separate Pods** whose existence is automatically managed by the operator.

```yaml
apiVersion: apps.pwlctk.ovh/v1alpha1
kind: Kroc
metadata:
  name: kroc-sample
spec:
  watchObject:
    apiVersion: apps/v1
    kind: Deployment
    nameRegex: pawel
    namespaceRegex: pawel
  resourceToCreate: |
    # Define variables for use in the template
    {{- $pod1 := 1 -}}
    {{- $pod2 := 2 -}}
    
    # --- Pod 1: Dynamically named and labeled using parent UID ---
    apiVersion: v1
    kind: Pod
    metadata:
      name: nginx-pod-{{ $pod1 }}
      namespace: pawel
      labels:
        environment: development
        # Inject attribute from the watched (parent) object
        parentuid: "{{.parent.metadata.uid}}"
    spec:
      containers:
      - name: nginx-container
        image: nginx:latest
        ports:
        - containerPort: 80
          protocol: TCP
    ---
    # --- Pod 2: Second object definition in the same template ---
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
        # Corrected image tag for example clarity
        image: nginx:latest 
        ports:
        - containerPort: 80 
          protocol: TCP
