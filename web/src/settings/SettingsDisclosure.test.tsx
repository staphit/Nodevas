import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SettingsDisclosure } from "./SettingsDisclosure";

// A row's value is cut with an ellipsis where the row is narrow ("Automatic
// (Engineeri…" at 150% text on a phone); its whole text shows on hover.
describe("SettingsDisclosure", () => {
  it("gives the row's value its whole text on hover", () => {
    render(
      <SettingsDisclosure label="Workflow stages" value="Automatic (Engineering)" open={false} onToggle={() => {}}>
        body
      </SettingsDisclosure>,
    );
    expect(screen.getByText("Automatic (Engineering)")).toHaveAttribute("title", "Automatic (Engineering)");
  });
});
