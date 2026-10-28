import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useProjectNodeSelection } from "./projectSelection";

// Node ids are per project (node-0001, node-0002, …), so the same id names a
// different card in every project. A selection carried across a project switch
// picked whatever card of the new project shared its id: "Selected node:
// Edited november" in a project where nothing had been clicked.
describe("the board's selected node", () => {
  it("starts empty", () => {
    const { result } = renderHook(() => useProjectNodeSelection("daily"));
    expect(result.current[0]).toBeNull();
  });

  it("is kept while the project stays", () => {
    const { result, rerender } = renderHook(({ project }) => useProjectNodeSelection(project), {
      initialProps: { project: "daily" },
    });
    act(() => result.current[1]("node-0004"));
    rerender({ project: "daily" });
    expect(result.current[0]).toBe("node-0004");
  });

  it("does not follow its id into another project", () => {
    const { result, rerender } = renderHook(({ project }) => useProjectNodeSelection(project), {
      initialProps: { project: "daily" },
    });
    act(() => result.current[1]("node-0004"));
    rerender({ project: "novel" });
    expect(result.current[0]).toBeNull();
  });

  it("is the new project's own once something there is selected", () => {
    const { result, rerender } = renderHook(({ project }) => useProjectNodeSelection(project), {
      initialProps: { project: "daily" },
    });
    act(() => result.current[1]("node-0004"));
    rerender({ project: "novel" });
    act(() => result.current[1]("node-0002"));
    expect(result.current[0]).toBe("node-0002");
  });

  // A select from the old project that lands after the switch (a late click
  // handler, a reveal) is the old project's and does not select here.
  it("ignores a select made for the project it left", () => {
    const { result, rerender } = renderHook(({ project }) => useProjectNodeSelection(project), {
      initialProps: { project: "daily" },
    });
    const selectInDaily = result.current[1];
    rerender({ project: "novel" });
    act(() => selectInDaily("node-0004"));
    expect(result.current[0]).toBeNull();
  });
});
