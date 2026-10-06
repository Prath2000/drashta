// three.js scene for the 3D model preview (ModelBody). Everything heavy is loaded on demand:
// `three` and the loaders are dynamic imports, so they are separate chunks that load only
// when someone opens a model.
//
// - mountScene fetches the model (abortable), parses it, then builds the renderer and returns a handle.
// - Nothing is fetched outside the project: every URL a model asks for goes through allowedUrl.
// - Rendering is on demand (a frame after the camera moves or the size changes), not a loop.
// - dispose() frees the geometries, materials, textures and the WebGL context. It is safe to call twice.
import type { BufferGeometry, Material, Mesh, Object3D, Texture, WebGLRenderer } from "three";
import { allowedUrl, fitDistance, type ModelKind } from "./fit";

export interface SceneHandle {
  dispose(): void;
  reset(): void;
  setWireframe(on: boolean): void;
  stats: ModelStats;
}

/** One mesh or point cloud of the model, for the polycount panel. */
export interface ModelPart {
  name: string;
  triangles: number;
  vertices: number;
}

/**
 * The polycount. meshes counts meshes and point clouds; triangles counts triangles of meshes only;
 * vertices are the vertices stored in the geometry buffers; materials and textures are distinct ones.
 * parts lists every mesh, most triangles first.
 */
export interface ModelStats {
  meshes: number;
  triangles: number;
  vertices: number;
  materials: number;
  textures: number;
  parts: ModelPart[];
}

/** Counts the polygons, vertices, materials and textures under `root`. */
export function countModel(root: Object3D): ModelStats {
  const stats: ModelStats = { meshes: 0, triangles: 0, vertices: 0, materials: 0, textures: 0, parts: [] };
  const materials = new Set<Material>();
  const textures = new Set<Texture>();
  root.traverse((o) => {
    const m = o as Object3D & { isMesh?: boolean; isPoints?: boolean; geometry?: BufferGeometry; material?: Material | Material[] };
    if (!m.isMesh && !m.isPoints) return;
    const g = m.geometry;
    const vertices = g?.getAttribute("position")?.count ?? 0;
    const corners = g ? (g.index ? g.index.count : vertices) : 0;
    const triangles = m.isMesh ? Math.floor(corners / 3) : 0;
    stats.meshes++;
    stats.triangles += triangles;
    stats.vertices += vertices;
    stats.parts.push({ name: m.name || g?.name || (m.isPoints ? "point cloud" : "mesh"), triangles, vertices });
    for (const mat of Array.isArray(m.material) ? m.material : m.material ? [m.material] : []) {
      materials.add(mat);
      for (const v of Object.values(mat)) if (v && (v as Texture).isTexture) textures.add(v as Texture);
    }
  });
  stats.materials = materials.size;
  stats.textures = textures.size;
  stats.parts.sort((a, b) => b.triangles - a.triangles || b.vertices - a.vertices);
  return stats;
}

const FOV = 45;
const BACKGROUND = "#0e0e0d";
const abortError = () => new DOMException("aborted", "AbortError");

/** Whether this browser can make a WebGL2 context (three.js needs WebGL2). The probe context is released at once. */
export function webglAvailable(): boolean {
  try {
    if (typeof document === "undefined") return false;
    const gl = document.createElement("canvas").getContext("webgl2");
    if (!gl) return false;
    gl.getExtension("WEBGL_lose_context")?.loseContext();
    return true;
  } catch {
    return false;
  }
}

/** Frees the geometry, materials and textures under `root`. */
function disposeTree(root: Object3D): void {
  root.traverse((o) => {
    const node = o as Object3D & { geometry?: { dispose(): void }; material?: Material | Material[] };
    node.geometry?.dispose();
    const mats = Array.isArray(node.material) ? node.material : node.material ? [node.material] : [];
    for (const m of mats) {
      for (const v of Object.values(m)) {
        if (v && (v as Texture).isTexture) (v as Texture).dispose();
      }
      m.dispose();
    }
  });
}

/** Reads the model file. The fetch stops when `signal` aborts. */
async function readBytes(url: string, base: string, signal: AbortSignal): Promise<ArrayBuffer> {
  if (!allowedUrl(url, base)) throw new Error("the model is outside the project files");
  const res = await fetch(url, { signal });
  if (!res.ok) throw new Error("the server answered " + res.status);
  return res.arrayBuffer();
}

export async function mountScene(canvas: HTMLCanvasElement, url: string, kind: ModelKind, base: string, signal: AbortSignal): Promise<SceneHandle> {
  if (signal.aborted) throw abortError();

  const THREE = await import("three");
  if (signal.aborted) throw abortError();
  const { OrbitControls } = await import("three/examples/jsm/controls/OrbitControls.js");
  if (signal.aborted) throw abortError();

  // A model can never fetch outside the project: other URLs become an empty data URL.
  const blocked: string[] = [];
  const manager = new THREE.LoadingManager();
  manager.setURLModifier((u) => {
    if (allowedUrl(u, base)) return u;
    blocked.push(u);
    return "data:,";
  });
  const onAbort = () => (manager as unknown as { abort?: () => void }).abort?.();
  signal.addEventListener("abort", onAbort, { once: true });

  let object: Object3D | null = null;
  try {
    const bytes = await readBytes(url, base, signal);
    if (signal.aborted) throw abortError();

    if (kind === "gltf") {
      const { GLTFLoader } = await import("three/examples/jsm/loaders/GLTFLoader.js");
      if (signal.aborted) throw abortError();
      const gltf = await new GLTFLoader(manager).parseAsync(bytes, THREE.LoaderUtils.extractUrlBase(url));
      object = gltf.scene;
    } else if (kind === "obj") {
      const { OBJLoader } = await import("three/examples/jsm/loaders/OBJLoader.js");
      if (signal.aborted) throw abortError();
      object = new OBJLoader(manager).parse(new TextDecoder().decode(bytes));
    } else {
      const geometry =
        kind === "stl"
          ? new (await import("three/examples/jsm/loaders/STLLoader.js")).STLLoader(manager).parse(bytes)
          : new (await import("three/examples/jsm/loaders/PLYLoader.js")).PLYLoader(manager).parse(bytes);
      if (signal.aborted) {
        geometry.dispose();
        throw abortError();
      }
      const colored = geometry.hasAttribute("color");
      if (kind === "ply" && !geometry.index) {
        // A PLY with no faces is a point cloud.
        object = new THREE.Points(geometry, new THREE.PointsMaterial({ size: 2, sizeAttenuation: false, vertexColors: colored, color: colored ? 0xffffff : 0xbfbfb8 }));
      } else {
        if (!geometry.hasAttribute("normal")) geometry.computeVertexNormals();
        object = new THREE.Mesh(
          geometry,
          new THREE.MeshStandardMaterial({ color: colored ? 0xffffff : 0xbfbfb8, vertexColors: colored, side: THREE.DoubleSide, roughness: 0.7, metalness: 0.1 }),
        );
      }
    }
    if (signal.aborted) throw abortError();
  } catch (e) {
    signal.removeEventListener("abort", onAbort);
    if (object) disposeTree(object);
    if (signal.aborted) throw abortError();
    // A file the model needed was refused, so the parser's own message (an empty buffer, a missing image) would be a riddle.
    if (blocked.length) throw new Error("the model asks for a file outside this project: " + blocked[0].slice(0, 120));
    throw e;
  }
  signal.removeEventListener("abort", onAbort);
  const model = object;

  // The model is parsed: count it. A model with no geometry has nothing to show.
  const stats = countModel(model);
  const scene = new THREE.Scene();
  scene.background = new THREE.Color(BACKGROUND);
  scene.add(model);
  const box = new THREE.Box3().setFromObject(model);
  const sphere = box.getBoundingSphere(new THREE.Sphere());
  if (box.isEmpty() || !(sphere.radius > 0) || !Number.isFinite(sphere.radius)) {
    disposeTree(model);
    throw new Error("the file has no geometry to show");
  }
  const size = box.getSize(new THREE.Vector3());
  model.position.sub(box.getCenter(new THREE.Vector3()));
  const radius = sphere.radius;

  // From here on the GL resources exist: nothing awaits, so nothing can abort in between.
  let renderer: WebGLRenderer;
  try {
    renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
  } catch (e) {
    disposeTree(model);
    throw e;
  }
  // Everything below runs without awaiting. If any step throws, the renderer, the observer, the
  // controls and the model are freed before the error goes on: nobody has a handle to dispose them.
  let disposed = false;
  let raf = 0;
  let observer: ResizeObserver | undefined;
  let controls: { dispose(): void } | undefined;
  try {
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));

    const grid = new THREE.GridHelper(Math.max(size.x, size.z, radius) * 2, 20, 0x3a3a36, 0x242422);
    grid.position.y = -size.y / 2 - radius * 0.002;
    scene.add(grid);
    scene.add(new THREE.HemisphereLight(0xffffff, 0x222222, 2.2));
    const sun = new THREE.DirectionalLight(0xffffff, 1.5);
    sun.position.set(radius * 2, radius * 3, radius * 2);
    scene.add(sun);

    const camera = new THREE.PerspectiveCamera(FOV, 1, radius / 100, radius * 200);
    const orbit = new OrbitControls(camera, canvas);
    controls = orbit;
    const heading = new THREE.Vector3(1, 0.8, 1).normalize();
    // Wireframe draws every mesh with ONE bright line material: the model's own colours (often dark
    // textures) made thin wires almost invisible. The real materials come back when it is turned off.
    const wireMaterial = new THREE.MeshBasicMaterial({ color: 0xd8d4c8, wireframe: true });
    const realMaterials = new Map<Mesh, Mesh["material"]>();

    const draw = () => {
      raf = 0;
      if (!disposed) renderer.render(scene, camera);
    };
    const requestRender = () => {
      if (!raf && !disposed) raf = requestAnimationFrame(draw);
    };

    const place = () => {
      const dist = fitDistance(radius, FOV, camera.aspect);
      camera.position.copy(heading).multiplyScalar(dist);
      camera.near = radius / 100;
      camera.far = (dist + radius) * 100;
      camera.updateProjectionMatrix();
      orbit.target.set(0, 0, 0);
      orbit.minDistance = radius * 0.05;
      orbit.maxDistance = dist * 10;
      orbit.update();
    };
    // The canvas can be 0x0 when the model finishes loading (a hidden or not yet laid out frame).
    // The first placement then used the default aspect, so the first real size places the camera again.
    let placed = false;
    let sized = false;
    const resize = () => {
      const w = canvas.clientWidth;
      const h = canvas.clientHeight;
      if (!w || !h) return;
      renderer.setSize(w, h, false);
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
      const first = !sized;
      sized = true;
      if (first && placed) place();
      requestRender();
    };
    const obs = new ResizeObserver(resize);
    observer = obs;
    obs.observe(canvas);
    resize();
    place();
    placed = true;
    orbit.addEventListener("change", requestRender);
    requestRender();

    return {
      stats,
      reset() {
        if (disposed) return;
        place();
        requestRender();
      },
      setWireframe(on) {
        if (disposed) return;
        if (on) {
          model.traverse((o) => {
            const mesh = o as Mesh;
            if (!mesh.isMesh || realMaterials.has(mesh)) return;
            realMaterials.set(mesh, mesh.material);
            mesh.material = wireMaterial;
          });
        } else {
          for (const [mesh, material] of realMaterials) mesh.material = material;
          realMaterials.clear();
        }
        requestRender();
      },
      dispose() {
        if (disposed) return;
        disposed = true;
        if (raf) cancelAnimationFrame(raf);
        obs.disconnect();
        orbit.removeEventListener("change", requestRender);
        orbit.dispose();
        // Put the real materials back first, so disposeTree frees them too.
        for (const [mesh, material] of realMaterials) mesh.material = material;
        realMaterials.clear();
        disposeTree(model);
        wireMaterial.dispose();
        grid.geometry.dispose();
        (grid.material as Material).dispose();
        renderer.dispose();
        renderer.forceContextLoss();
      },
    };
  } catch (e) {
    disposed = true;
    if (raf) cancelAnimationFrame(raf);
    observer?.disconnect();
    controls?.dispose();
    disposeTree(scene); // the model and the grid
    renderer.dispose();
    renderer.forceContextLoss();
    throw e;
  }
}
