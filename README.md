# spire-k8s-registrar

A replacement registrar for issuing SVIDs to k8s workloads.

The creation of registration entries is intended to be primarily done through creation of ClusterSpiffeId resource and a yet to be implemented namespaced SpiffeId resource.
Spire registration entries will be created with appropriate selectors to match those in the ClusterSpiffeId object. At present the selector mapping is 1:1, but in future that may cease to be the case.

There is an optional pod controller which emulates the original k8s-registrar behaviours. The pod controller watches for pod labels/annotations and will automatically create and destroy ClusterSpiffeId resources for them.

## The SpireEntry resource and the delights of garbage collection
These resources are not intended to be managed by humans!

Spire server will not allow you to create two identical entries. This poses us a problem because two different ClusterSpiffeIds (and, more likely, the future SpiffeId resource) could end up mapping to an identical entry.
If we simply tracked spire server entry IDs on ClusterSpiffeId's status we would have difficulty working out when the entry can be freed as multiple resources could have the same entry ID.
To work around this we add an extra layer, the SpireEntry resource. ClusterSpiffeIDs generate SpireEntry resources with a predictable name. Multiple ClusterSpiffeIds can reference the same SpireEntry, but SpireEntry to the actual Spire Entries is guaranteed to be a 1:1 mapping.
It's then possible to use ownerReferences to trigger the k8s GC to automatically cascade deletes to SpireEntry when the last ClusterSpiffeIds using it is deleted.

This approach has several shortcomings:
* It's complicated
* The current naming for SpireEntry is a simple hash over the spec of ClusterSpiffeId. Hash collisions are possible, and would be very bad.
* It adds a further delay to creating spire entries as it's another layer of controller reconcilation that needs to happen.
