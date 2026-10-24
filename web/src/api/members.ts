import { req } from "./http";

// Mirrors internal/httpapi/members. A project with no members is open to every
// signed-in account; once it has members, only they (and server
// administrators) reach it, each as their role allows.

export type MemberRole = "owner" | "editor" | "viewer";

export interface ProjectMember {
  userId: string;
  name: string;
  role: MemberRole;
  addedBy?: string;
  createdAt: string;
}

export interface ProjectMembers {
  project: string;
  restricted: boolean;
  members: ProjectMember[];
}

export interface ProjectAccess {
  project: string;
  restricted: boolean;
  /** The role the caller acts with: owner for an administrator, editor for
   * anyone on an open project. */
  role: MemberRole | "";
  /** The caller's own row, "" when they are not on the list. */
  member: MemberRole | "";
  admin: boolean;
  canWrite: boolean;
  canManage: boolean;
}

const q = (project: string) =>
  project ? `?project=${encodeURIComponent(project)}` : "";

export const membersApi = {
  projectAccess: (project: string) =>
    req<ProjectAccess>(`/api/projects/access${q(project)}`),
  listMembers: (project: string) =>
    req<ProjectMembers>(`/api/projects/members${q(project)}`),
  /** user is an account's exact name or sign-in address. */
  setMember: (project: string, user: string, role: MemberRole) =>
    req<ProjectMembers>("/api/projects/members", {
      method: "PUT",
      body: JSON.stringify({ project, user, role }),
    }),
  setMemberRole: (project: string, userId: string, role: MemberRole) =>
    req<ProjectMembers>("/api/projects/members", {
      method: "PUT",
      body: JSON.stringify({ project, userId, role }),
    }),
  removeMember: (project: string, userId: string) =>
    req<ProjectMembers>(
      `/api/projects/members?project=${encodeURIComponent(project)}&userId=${encodeURIComponent(userId)}`,
      { method: "DELETE" },
    ),
  openProjectToEveryone: (project: string) =>
    req<ProjectMembers>("/api/projects/members/open", {
      method: "POST",
      body: JSON.stringify({ project }),
    }),
};
