import { render } from "@testing-library/react";
import { describe, expect, test } from "vitest";
import { MaterialFilledTextField, MaterialOutlinedTextField } from "./MaterialTextField";

describe("MaterialTextField", () => {
  test("marks single-line outlined text fields with the compact density class", () => {
    const { container } = render(
      <MaterialOutlinedTextField
        className="external-field"
        label="Model"
        value="moonbridge"
        onInput={() => undefined}
      />
    );

    const field = getOutlinedTextField(container, "Model");

    expect(field).toHaveClass("external-field");
    expect(field).toHaveClass("material-text-field--single-line");
  });

  test("serializes the Material label on the host element", () => {
    const { container } = render(
      <MaterialOutlinedTextField
        label="Max uses"
        value=""
        onInput={() => undefined}
      />
    );

    expect(getOutlinedTextField(container, "Max uses")).toHaveAttribute("data-label", "Max uses");
  });

  test("marks single-line filled text fields with the compact density class", () => {
    const { container } = render(
      <MaterialFilledTextField
        className="auth-token-field"
        label="Token"
        type="password"
        value=""
        onInput={() => undefined}
      />
    );

    const field = getFilledTextField(container, "Token");

    expect(field).toHaveClass("auth-token-field");
    expect(field).toHaveClass("material-text-field--single-line");
  });

  test("keeps textarea text fields out of the single-line density class", () => {
    const { container } = render(
      <MaterialOutlinedTextField
        label="Input"
        rows={6}
        type="textarea"
        value="ping"
        onInput={() => undefined}
      />
    );

    expect(getOutlinedTextField(container, "Input")).not.toHaveClass("material-text-field--single-line");
  });
});

type MaterialTextFieldElement = HTMLElement & {
  label: string;
};

function getOutlinedTextField(container: ParentNode, label: string) {
  const field = Array.from(container.querySelectorAll<MaterialTextFieldElement>(".bh-field:not(.bh-select)")).find(
    (candidate) => candidate.getAttribute("data-label") === label
  );
  if (!field) {
    throw new Error(`Expected outlined text field labelled "${label}".`);
  }
  return field;
}

function getFilledTextField(container: ParentNode, label: string) {
  const field = Array.from(container.querySelectorAll<MaterialTextFieldElement>(".bh-field:not(.bh-select)")).find(
    (candidate) => candidate.getAttribute("data-label") === label
  );
  if (!field) {
    throw new Error(`Expected filled text field labelled "${label}".`);
  }
  return field;
}
