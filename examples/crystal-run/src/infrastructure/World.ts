import * as THREE from "three";
import { crystals, platforms, portal, hazardAt } from "../gameplay/level";
import type { Player } from "../gameplay/Player";
import type { Run } from "../domain/Run";

export class World {
  private renderer: THREE.WebGLRenderer;
  private scene = new THREE.Scene();
  private camera = new THREE.PerspectiveCamera(52, 1, 0.1, 180);
  private player = new THREE.Mesh(
    new THREE.CapsuleGeometry(0.35, 0.65, 4, 8),
    new THREE.MeshStandardMaterial({ color: 0xffe3a6, roughness: 0.35 }),
  );
  private gems = new Map<string, THREE.Mesh>();
  private gate = new THREE.Mesh(
    new THREE.TorusGeometry(1.15, 0.16, 12, 40),
    new THREE.MeshStandardMaterial({ color: 0x777799, emissive: 0x111122 }),
  );
  private hazard = new THREE.Mesh(
    new THREE.IcosahedronGeometry(0.7),
    new THREE.MeshStandardMaterial({ color: 0xff5577, emissive: 0x661122 }),
  );
  private resize = () => {
    this.renderer.setSize(window.innerWidth, window.innerHeight);
    this.camera.aspect = window.innerWidth / window.innerHeight;
    this.camera.updateProjectionMatrix();
  };
  constructor(canvas: HTMLCanvasElement) {
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
    this.renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    this.scene.background = new THREE.Color(0x091427);
    this.scene.fog = new THREE.Fog(0x091427, 35, 100);
    this.scene.add(new THREE.HemisphereLight(0xb9eaff, 0x30234e, 2.8));
    const sun = new THREE.DirectionalLight(0xffedc4, 3);
    sun.position.set(6, 12, 4);
    this.scene.add(sun);
    const islandMaterial = new THREE.MeshStandardMaterial({
      color: 0x247b80,
      roughness: 0.8,
    });
    for (const p of platforms) {
      const island = new THREE.Mesh(
        new THREE.BoxGeometry(p.w, 1.4, p.d),
        islandMaterial,
      );
      island.position.set(p.x, -0.7, p.z);
      this.scene.add(island);
      const base = new THREE.Mesh(
        new THREE.ConeGeometry(Math.min(p.w, p.d) * 0.6, 4, 5),
        new THREE.MeshStandardMaterial({ color: 0x203650 }),
      );
      base.rotation.z = Math.PI;
      base.position.set(p.x, -3.2, p.z);
      this.scene.add(base);
    }
    const gemGeometry = new THREE.OctahedronGeometry(0.55);
    const gemMaterial = new THREE.MeshStandardMaterial({
      color: 0x85fff0,
      emissive: 0x19a99a,
      emissiveIntensity: 0.8,
      metalness: 0.4,
      roughness: 0.2,
    });
    for (const c of crystals) {
      const gem = new THREE.Mesh(gemGeometry, gemMaterial);
      gem.position.set(c.x, 1.3, c.z);
      this.gems.set(c.id, gem);
      this.scene.add(gem);
    }
    this.gate.position.set(portal.x, 1.5, portal.z);
    this.scene.add(this.player, this.gate, this.hazard);
    window.addEventListener("resize", this.resize);
    this.resize();
  }
  render(player: Player, run: Run, dt: number) {
    this.player.position.set(player.x, player.y + 0.7, player.z);
    for (const [id, gem] of this.gems) {
      gem.visible = !run.has(id);
      gem.rotation.y = run.seconds * 1.5;
      gem.position.y = 1.3 + Math.sin(run.seconds * 2 + gem.position.z) * 0.15;
    }
    this.gate.material.color.set(run.portalOpen ? 0x83fff0 : 0x777799);
    this.gate.material.emissive.set(run.portalOpen ? 0x229988 : 0x111122);
    const h = hazardAt(run.seconds);
    this.hazard.position.set(h.x, 0.8, h.z);
    this.hazard.rotation.y = run.seconds;
    this.camera.position.lerp(
      new THREE.Vector3(player.x, player.y + 11, player.z + 14),
      1 - Math.exp(-6 * dt),
    );
    this.camera.lookAt(player.x, player.y, player.z - 4);
    this.renderer.render(this.scene, this.camera);
  }
  dispose() {
    window.removeEventListener("resize", this.resize);
    const geometries = new Set<THREE.BufferGeometry>();
    const materials = new Set<THREE.Material>();
    this.scene.traverse((object) => {
      if (object instanceof THREE.Mesh) {
        geometries.add(object.geometry);
        const values = Array.isArray(object.material)
          ? object.material
          : [object.material];
        values.forEach((m) => materials.add(m));
      }
    });
    geometries.forEach((g) => g.dispose());
    materials.forEach((m) => m.dispose());
    this.renderer.dispose();
  }
}
