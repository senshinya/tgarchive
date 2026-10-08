/** Scrolls `list`, and nothing else, so that `el` sits at its top (below el's scroll-margin-top) or
 * in its middle. Element.scrollIntoView also scrolls every scrollable ancestor: on phones that is
 * the wrapper sliding the columns, which it shifted sideways. */
export function scrollWithin(
  list: HTMLElement,
  el: HTMLElement,
  block: 'start' | 'center',
  style: (el: HTMLElement) => CSSStyleDeclaration = getComputedStyle,
) {
  const offset = el.getBoundingClientRect().top - list.getBoundingClientRect().top;
  const margin = block === 'start' ? parseFloat(style(el).scrollMarginTop) || 0 : (list.clientHeight - el.offsetHeight) / 2;
  list.scrollTop += offset - margin;
}
