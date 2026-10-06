import { type RouteConfig, index, route } from "@react-router/dev/routes";

// URL map (04-data-model §7):
//   /                     home (seismograph)
//   /p                    project picker (no project chosen yet)
//   /p/:project/:tab?     project page
//   /tracker?filter=review
//   /inbox
// Any route may carry ?item=<ref> to open the viewport drawer.
export default [
  index("routes/home.tsx"),
  route("p", "routes/picker.tsx"),
  route("p/:project/:tab?", "routes/project.tsx"),
  route("tracker", "routes/tracker.tsx"),
  route("inbox", "routes/inbox.tsx"),
  route("*", "routes/not-found.tsx"),
] satisfies RouteConfig;
