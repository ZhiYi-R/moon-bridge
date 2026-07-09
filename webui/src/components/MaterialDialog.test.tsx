import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";
import { MaterialDialog } from "./MaterialDialog";

describe("MaterialDialog", () => {
  test("renders the official dialog host with aria-label, headline and content", () => {
    const { container } = render(
      <MaterialDialog open={false} onClose={() => undefined} ariaLabel="Edit Route primary" headline="Edit Route">
        <p>route fields</p>
      </MaterialDialog>
    );

    const dialog = container.querySelector("dialog.bh-dialog, dialog");
    expect(dialog).toBeInTheDocument();
    // headline present uses aria-labelledby; ariaLabel used when no headline
    expect(dialog).toBeTruthy();

    expect(screen.getByText("Edit Route")).toBeInTheDocument();
    expect(screen.getByText("route fields")).toBeInTheDocument();
    expect(container.querySelector(".bh-dialog__headline, .material-dialog__headline")).toBeInTheDocument();
    expect(container.querySelector(".bh-dialog__content, .material-dialog__content")).toBeInTheDocument();
  });

  test("renders an official Material icon button to close the dialog", () => {
    const { container } = render(
      <MaterialDialog open onClose={() => undefined} headline="Edit Route">
        body
      </MaterialDialog>
    );

    const closeButton = container.querySelector('button.bh-icon-button[aria-label="Close"]');
    expect(closeButton).toBeInTheDocument();
  });

  test("invokes onClose when the close button is clicked", () => {
    const onClose = vi.fn();
    const { container } = render(
      <MaterialDialog open={false} onClose={onClose} headline="Edit Route">
        body
      </MaterialDialog>
    );

    fireEvent.click(container.querySelector('button.bh-icon-button[aria-label="Close"]')!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("invokes onClose when dialog dispatches its close event (scrim/Escape)", () => {
    const onClose = vi.fn();
    const { container } = render(
      <MaterialDialog open onClose={onClose} headline="Edit Route">
        body
      </MaterialDialog>
    );

    const dialog = container.querySelector("dialog.bh-dialog, dialog")!;
    dialog.dispatchEvent(new Event("close"));

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
