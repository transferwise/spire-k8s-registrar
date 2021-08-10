# spire-k8s-registrar

**NOTE: This repository is now archived, with its features upstreamed into https://github.com/spiffe/spire/blob/main/support/k8s/k8s-workload-registrar/. See https://docs.google.com/document/d/1DGHVw3n3w1YrXzGazjx-Qw6Mi44eoaSUJyqQuRqbyec for history.**

A replacement registrar for issuing SVIDs to k8s workloads.

The creation of registration entries is intended to be primarily done through creation of ClusterSpiffeId resource and a yet to be implemented namespaced SpiffeId resource.
Spire registration entries will be created with appropriate selectors to match those in the ClusterSpiffeId object. At present the selector mapping is 1:1, but in future that may cease to be the case.

This was intended to allow upfront creation and management of spire server entries as resources, rather than having to create them on the fly as pods are created. This has the upside of removing a point of falure at pod admission, and removing any problems with delays in SVIDs being issued after admisson. The cost is the loss of a unique per-pod identity (instead you'd issue identities per namespace or service account.)

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

Alternatives:
* Ignore the problem, allow "undefined" behaviour on a collision (spiffe id resources that think they have an entry, but don't.)
* Accept a brief period where a collided entry may vanish, but get recreated. There are a couple of simple ways to implement this. In the event of a collision, when the winning resource is removed the spire entry will vanish and the losing resource will trigger its recreation.
* Modify spire server to allow colliding entries (seems ugly)
