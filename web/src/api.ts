/** One-shot HTTP helpers for the non-streaming backend endpoints. */
import type { Config, DirListing, GitInfo, PickResult, Profile } from "./protocol";

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return (await res.json()) as T;
}

export const api = {
  health: () => get<{ version: string; protocol: number; uptime: number; configPath: string }>("/api/health"),
  config: () => get<Config>("/api/config"),
  async saveConfig(cfg: Config): Promise<Config> {
    const res = await fetch("/api/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(cfg),
    });
    if (!res.ok) throw new Error(`config save: ${res.status}`);
    return (await res.json()) as Config;
  },
  profiles: () => get<Profile[]>("/api/profiles"),
  fonts: () => get<string[]>("/api/fonts"),
  git: (path: string) => get<GitInfo>(`/api/git?path=${encodeURIComponent(path)}`),
  dir: (path: string) => get<DirListing>(`/api/dir?path=${encodeURIComponent(path)}`),
  reveal: (path: string) => get<{ status: string }>(`/api/reveal?path=${encodeURIComponent(path)}`),
  open: (path: string) => get<{ status: string }>(`/api/open?path=${encodeURIComponent(path)}`),
  pick: (kind: "folder" | "file", title: string) =>
    get<PickResult>(`/api/pick?kind=${kind}&title=${encodeURIComponent(title)}`),
};
