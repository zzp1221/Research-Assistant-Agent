export interface LayoutRect { left: number; top: number; right: number; bottom: number }
export interface PopoverLayout { left: number; top: number; width: number; height: number }
export function intersectRects(...rects: LayoutRect[]): LayoutRect | null {
  const r = {
    left: Math.max(...rects.map((rect) => rect.left)), top: Math.max(...rects.map((rect) => rect.top)),
    right: Math.min(...rects.map((rect) => rect.right)), bottom: Math.min(...rects.map((rect) => rect.bottom)),
  };
  return r.right > r.left && r.bottom > r.top ? r : null;
}
const clamp = (value: number, low: number, high: number) => Math.min(Math.max(value, low), high);
export function computePopoverLayout(
  visible: LayoutRect, selection: LayoutRect, desiredHeight: number, chromeHeight: number,
): PopoverLayout | null {
  if (!intersectRects(visible, selection)) return null;
  const marginX = Math.min(8, (visible.right - visible.left) / 10);
  const marginY = Math.min(8, (visible.bottom - visible.top) / 10);
  const bounds = { left: visible.left + marginX, right: visible.right - marginX, top: visible.top + marginY, bottom: visible.bottom - marginY };
  const width = Math.min(360, bounds.right - bounds.left);
  const available = bounds.bottom - bounds.top;
  // A header and close control must fit; never impose a minimum on the box.
  if (width < 80 || available < chromeHeight) return null;
  const height = Math.min(Math.max(chromeHeight, desiredHeight), available);
  const below = Math.max(0, bounds.bottom - selection.bottom - 8);
  const above = Math.max(0, selection.top - 8 - bounds.top);
  let top = selection.bottom + 8;
  if (below < height && above > below) top = selection.top - 8 - height;
  return { left: clamp(selection.left, bounds.left, bounds.right - width), top: clamp(top, bounds.top, bounds.bottom - height), width, height };
}

// All coordinates are viewport coordinates, including visualViewport offsets.
export function readVisibleReaderRect(root: HTMLElement, body: HTMLElement): LayoutRect | null {
  const viewport = window.visualViewport;
  // default-layout 3.12 puts the absolute toolbar INSIDE body and reserves
  // padding-top for it. body's border box alone still includes the toolbar.
  const bodyRect = body.getBoundingClientRect();
  const toolbar = root.querySelector<HTMLElement>('.rpv-default-layout__toolbar');
  const content = root.querySelector<HTMLElement>('.rpv-core__inner-pages');
  const paddingTop = parseFloat(getComputedStyle(body).paddingTop) || 0;
  const rects: LayoutRect[] = [root.getBoundingClientRect(), {
    left: bodyRect.left, right: bodyRect.right, bottom: bodyRect.bottom,
    top: Math.max(bodyRect.top + paddingTop, toolbar?.getBoundingClientRect().bottom ?? bodyRect.top),
  }, {
    left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight,
  }];
  if (content) rects.push(content.getBoundingClientRect());
  if (viewport) rects.push({ left: viewport.offsetLeft, top: viewport.offsetTop, right: viewport.offsetLeft + viewport.width, bottom: viewport.offsetTop + viewport.height });
  for (let element: HTMLElement | null = root.parentElement; element; element = element.parentElement) {
    const style = getComputedStyle(element);
    if (style.display === 'none' || style.visibility === 'hidden') return null;
    const rect = element.getBoundingClientRect();
    const clipsX = /(hidden|clip|auto|scroll)/.test(style.overflowX);
    const clipsY = /(hidden|clip|auto|scroll)/.test(style.overflowY);
    if (clipsX || clipsY) rects.push({ left: clipsX ? rect.left : -Infinity, right: clipsX ? rect.right : Infinity, top: clipsY ? rect.top : -Infinity, bottom: clipsY ? rect.bottom : Infinity });
  }
  if (getComputedStyle(root).visibility === 'hidden') return null;
  return intersectRects(...rects);
}
