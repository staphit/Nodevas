import { describe, expect, it } from "vitest";
import {
  advancedConditionKind,
  hasAdvancedCondition,
  isPlainAllOfRequirement,
} from "./simpleDependency";

describe("isPlainAllOfRequirement", () => {
  it("treats no condition, one reference and an AND chain as the default", () => {
    expect(isPlainAllOfRequirement(undefined)).toBe(true);
    expect(isPlainAllOfRequirement("  ")).toBe(true);
    expect(isPlainAllOfRequirement("design")).toBe(true);
    expect(isPlainAllOfRequirement("design and review and node-3")).toBe(true);
    expect(isPlainAllOfRequirement("design AND review")).toBe(true);
  });

  it("treats every other operator, flag or group as advanced", () => {
    expect(isPlainAllOfRequirement("design or review")).toBe(false);
    expect(isPlainAllOfRequirement("design xor review")).toBe(false);
    expect(isPlainAllOfRequirement("not (design and review)")).toBe(false);
    expect(isPlainAllOfRequirement("design nor review")).toBe(false);
    expect(isPlainAllOfRequirement("design and flag(approved)")).toBe(false);
    expect(isPlainAllOfRequirement("(design or review) and ship")).toBe(false);
    expect(isPlainAllOfRequirement("not design")).toBe(false);
    expect(isPlainAllOfRequirement("true")).toBe(false);
  });
});

describe("hasAdvancedCondition", () => {
  it("flags optional and deprecated prerequisites even under a plain AND", () => {
    expect(hasAdvancedCondition("a and b", [{ from: "a", to: "t" }, { from: "b", to: "t" }])).toBe(false);
    expect(
      hasAdvancedCondition("a", [
        { from: "a", to: "t" },
        { from: "b", to: "t", relation: "optional" },
      ]),
    ).toBe(true);
    expect(hasAdvancedCondition("a or b", [])).toBe(true);
  });
});

describe("advancedConditionKind", () => {
  it("names the operator, or calls anything else custom", () => {
    expect(advancedConditionKind("a and b")).toBeNull();
    expect(advancedConditionKind("a or b")).toBe("or");
    expect(advancedConditionKind("a xor b")).toBe("xor");
    expect(advancedConditionKind("a nand b")).toBe("nand");
    expect(advancedConditionKind("not (a or b)")).toBe("not");
    expect(advancedConditionKind("a and flag(ok)")).toBe("custom");
  });
});
