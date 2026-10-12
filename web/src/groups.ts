// How the sidebar groups conversations.
//
// A conversation sits in its project's group ("p:<folder name>") unless the
// user moved it: into a group of their own (an id from /api/groups), or into
// another project's group. Pinned conversations are listed once, in Pinned.

import type { ConvGroup, SessionSummary } from "./api";

export const PROJECT_PREFIX = "p:";

export interface GroupSection {
  key: string; // "p:<project>" or a custom group's id
  name: string;
  kind: "project" | "custom";
  sessions: SessionSummary[]; // the conversations shown under it (pinned ones are in Pinned)
  total: number; // everything in the group, pinned included
  latest: number; // newest update, for ordering project groups
}

export interface MoveTarget {
  key: string;
  name: string;
  kind: "project" | "custom";
}

/** The group a conversation belongs to right now. */
export function groupKeyOf(s: SessionSummary, custom: Set<string>): string {
  if (s.group && (s.group.startsWith(PROJECT_PREFIX) || custom.has(s.group))) return s.group;
  return ownGroupKey(s);
}

/** The group its own project gives it, where "Move back" returns it. */
export function ownGroupKey(s: SessionSummary): string {
  return PROJECT_PREFIX + (s.project || "");
}

export function groupNameOf(key: string, custom: ConvGroup[]): string {
  if (key.startsWith(PROJECT_PREFIX)) return key.slice(PROJECT_PREFIX.length) || "No project";
  return custom.find((g) => g.id === key)?.name ?? key;
}

export function buildSections(
  sessions: SessionSummary[],
  custom: ConvGroup[],
  searching: boolean,
): { pinned: SessionSummary[]; groups: GroupSection[] } {
  const customIds = new Set(custom.map((g) => g.id));
  const byKey = new Map<string, GroupSection>();
  const section = (key: string): GroupSection => {
    let g = byKey.get(key);
    if (!g) {
      g = {
        key,
        name: groupNameOf(key, custom),
        kind: key.startsWith(PROJECT_PREFIX) ? "project" : "custom",
        sessions: [],
        total: 0,
        latest: 0,
      };
      byKey.set(key, g);
    }
    return g;
  };
  const pinned: SessionSummary[] = [];
  for (const s of sessions) {
    section(ownGroupKey(s)); // a project's group stays, even when everything in it was moved elsewhere
    const g = section(groupKeyOf(s, customIds));
    g.total++;
    g.latest = Math.max(g.latest, new Date(s.updated).getTime() || 0);
    if (s.pinned) pinned.push(s);
    else g.sessions.push(s);
  }
  // User-made groups always show (empty ones too), except while searching.
  if (!searching) custom.forEach((g) => section(g.id));
  const all = [...byKey.values()].filter((g) => !searching || g.sessions.length > 0);
  const projects = all.filter((g) => g.kind === "project").sort((a, b) => b.latest - a.latest);
  const mine = custom.map((c) => all.find((g) => g.key === c.id)).filter((g): g is GroupSection => !!g);
  return { pinned, groups: [...projects, ...mine] };
}

/** Every group a conversation can be moved to: the projects that have conversations, then the user's groups. */
export function moveTargets(sessions: SessionSummary[], custom: ConvGroup[]): MoveTarget[] {
  const seen = new Set<string>();
  const projects: MoveTarget[] = [];
  for (const s of sessions) {
    const key = ownGroupKey(s);
    const moved = s.group?.startsWith(PROJECT_PREFIX) ? s.group : null;
    for (const k of [key, moved]) {
      if (k && !seen.has(k)) {
        seen.add(k);
        projects.push({ key: k, name: groupNameOf(k, custom), kind: "project" });
      }
    }
  }
  return [...projects, ...custom.map((g) => ({ key: g.id, name: g.name, kind: "custom" as const }))];
}
