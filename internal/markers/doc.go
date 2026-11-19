// Package markers follows VS Code's MarkerService
// (src/vs/platform/markers/common/markerService.ts). One owner's markers for
// one resource are replaced atomically with ChangeOne; Read answers with the
// current markers; OnMarkerChanged reports the resources whose markers
// changed.
//
// Resources are strings here instead of VS Code URIs. The app uses a
// vscroxy: scheme to name its request documents and its import report.
// Unlike the immediate-mode UI, the event exists because markers are produced
// on send workers and consumed by the workbench; listeners can be owned by a
// lifecycle.DisposableStore.
package markers
