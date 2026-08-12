import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import {
  degradedDocumentsReducer,
  DocumentPersistenceBanner,
  documentPersistenceAction,
} from "./DocumentPersistenceBanner";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
  useApp.getState().updateUIPreference("language", "zh-TW");
});

describe("document persistence events", () => {
  it("tracks each degraded document until its own restored event arrives", () => {
    let state: ReadonlySet<string> = new Set();
    for (const event of [
      { type: "doc-persistence-error" as const, id: "alpha" },
      { type: "doc-persistence-error" as const, id: "beta/page" },
      { type: "doc-persistence-error" as const, id: "alpha" },
    ]) {
      const action = documentPersistenceAction(event);
      if (action) state = degradedDocumentsReducer(state, action);
    }
    expect([...state]).toEqual(["alpha", "beta/page"]);

    const restored = documentPersistenceAction({
      type: "doc-persistence-restored",
      id: "alpha",
    });
    if (restored) state = degradedDocumentsReducer(state, restored);
    expect([...state]).toEqual(["beta/page"]);

    const final = documentPersistenceAction({
      type: "doc-persistence-restored",
      id: "beta/page",
    });
    if (final) state = degradedDocumentsReducer(state, final);
    expect(state.size).toBe(0);
  });

  it("clears stale warnings when the socket generation or project changes", () => {
    const state = degradedDocumentsReducer(new Set<string>(), {
      type: "degraded",
      id: "alpha",
    });
    expect(degradedDocumentsReducer(state, { type: "reset" }).size).toBe(0);
  });

  it("ignores durability events without a sanitized document id", () => {
    expect(
      documentPersistenceAction({ type: "doc-persistence-error", id: "" }),
    ).toBeNull();
    expect(documentPersistenceAction({ type: "presence", id: "alpha" })).toBeNull();
  });
});

describe("DocumentPersistenceBanner", () => {
  it("is a sticky, non-dismissible alert with recovery instructions", () => {
    const { rerender } = render(
      <DocumentPersistenceBanner documents={new Set(["alpha", "beta"])} />,
    );

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("伺服器目前無法保護未儲存的協作內容");
    expect(alert).toHaveTextContent("保持此分頁開啟");
    expect(alert).toHaveTextContent("匯出或複製內容");
    expect(alert).toHaveTextContent("重試儲存");
    expect(alert.querySelector("button")).toBeNull();

    rerender(<DocumentPersistenceBanner documents={new Set()} />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
