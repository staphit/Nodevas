import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ConfirmOptions } from "../components/ConfirmDialog";
import { translate } from "../i18n";
import { confirmApply } from "./confirmWorkspace";

const confirm = vi.hoisted(() => ({ action: vi.fn(async (_options: unknown) => true) }));
vi.mock("../components/ConfirmDialog", () => ({ confirmAction: confirm.action }));

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);
const asked = () => confirm.action.mock.calls[0][0] as ConfirmOptions;

beforeEach(() => confirm.action.mockClear());

describe("confirmApply", () => {
  it("names the folder the result goes into, in plain words", async () => {
    expect(await confirmApply(t, "my-bakery")).toBe(true);
    const options = asked();
    expect(options.title).toBe("Apply this result to your folder?");
    expect(options.description).toContain("my-bakery");
    expect(options.description).toContain("Nothing is sent outside this computer.");
    expect(options.confirmLabel).toBe("Apply");
    expect(`${options.title} ${options.description}`).not.toMatch(/merge|branch|commit/i);
  });

  it('says "your folder" when the folder has no name', async () => {
    confirm.action.mockResolvedValueOnce(false);
    expect(await confirmApply(t, "")).toBe(false);
    expect(asked().description).toContain("will be put into your folder.");
  });
});
