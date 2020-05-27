# spire-k8s-registrar

A replacement registrar for issuing SVIDs to k8s workloads.

The creation of registration entries is intended to be primarily done through creation of ClusterSpiffeId resource and a yet to be implemented namespaced SpiffeId resource.
Spire registration entries will be created with appropriate selectors to match those in the ClusterSpiffeId object. At present the selector mapping is 1:1, but in future that may cease to be the case.

This was intended to allow upfront creation and management of spire server entries as resources, rather than having to create them on the fly as pods are created. This has the upside of removing a point of falure at pod admission, and removing any problems with delays in SVIDs being issued after admisson. The cost is the loss of a unique per-pod identity (instead you'd issue identities per namespace or service account.)

There is an optional pod controller which emulates the original k8s-registrar behaviours. The pod controller watches for pod labels/annotations and will automatically create and destroy ClusterSpiffeId resources for them.

