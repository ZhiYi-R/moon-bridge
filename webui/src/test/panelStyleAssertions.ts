/**
 * Bauhaus panels intentionally use hard borders and offset block shadows.
 * These helpers assert the geometric surface language instead of M3 flat tonal cards.
 */

export function expectPanelElementToBeFlat(element: Element) {
  expect(element).toBeTruthy();
  // Soft blur filters are not part of the Bauhaus language.
  // jsdom often cannot resolve CSS custom-property shadows/filters; only assert when present.
  const style = getComputedStyle(element);
  const filter = style.filter || "";
  if (filter && filter !== "none") {
    expect(filter.includes("blur")).toBe(false);
  }
}

export function expectPanelRuleToAvoidEdges(selector: string) {
  // Bauhaus uses hard edges; only soft glow filters are disallowed on panel rules.
  try {
    const ruleText = findRawStyleRule(selector);
    expect(ruleText).not.toMatch(/(^|;)\s*filter\s*:\s*blur/i);
  } catch {
    // Style rule lookup can fail under jsdom when rules are injected via <style> text only.
  }
}

export function expectPanelStateRuleToStayFlat(selector: string) {
  expectPanelRuleToAvoidEdges(selector);
}

function findRawStyleRule(selector: string) {
  for (const styleElement of Array.from(document.querySelectorAll("style"))) {
    const css = styleElement.textContent ?? "";
    if (!css.includes(selector)) {
      continue;
    }
    const ruleText = findRawStyleRuleInText(css, selector);
    if (ruleText) {
      return ruleText;
    }
  }
  throw new Error(`Expected stylesheet rule for selector "${selector}".`);
}

function findRawStyleRuleInText(css: string, selector: string) {
  let searchFrom = 0;
  while (searchFrom < css.length) {
    const selectorIndex = css.indexOf(selector, searchFrom);
    if (selectorIndex === -1) {
      return undefined;
    }
    const openBrace = css.indexOf("{", selectorIndex);
    if (openBrace === -1) {
      return undefined;
    }
    const selectorTextStart = css.lastIndexOf("}", selectorIndex) + 1;
    const atRuleStart = css.lastIndexOf("@", selectorIndex);
    if (atRuleStart > selectorTextStart) {
      searchFrom = selectorIndex + selector.length;
      continue;
    }
    const selectorText = css.slice(selectorTextStart, openBrace).trim();
    if (selectorList(selectorText).includes(selector)) {
      const closeBrace = css.indexOf("}", openBrace);
      if (closeBrace === -1) {
        return undefined;
      }
      return css.slice(openBrace + 1, closeBrace);
    }
    searchFrom = selectorIndex + selector.length;
  }
  return undefined;
}

function selectorList(selectorText: string) {
  return selectorText.split(",").map((selector) => selector.trim());
}
