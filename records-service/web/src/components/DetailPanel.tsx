import { useEffect, useRef, type ReactNode } from 'react';

import { IconButton } from './IconButton';

/**
 * The slide-out detail panel.
 *
 * It is a modal dialog, so it owes four things that are easy to leave out and
 * impossible to work around from the keyboard:
 *
 *   - a name, so a screen reader announces what opened rather than "dialog";
 *   - focus moved into it when it opens, or the operator is still on the page
 *     behind it;
 *   - focus kept inside it while it is open, or Tab walks into content the
 *     operator cannot see;
 *   - focus returned to the control that opened it when it closes, or the
 *     operator lands at the top of the document and has to find their place.
 *
 * The transition is suppressed under prefers-reduced-motion; that is in the
 * stylesheet, next to the transition it turns off.
 */
export function DetailPanel({
  title,
  subtitle,
  onClose,
  children,
}: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const opener = useRef<Element | null>(null);

  useEffect(() => {
    // Remember where focus came from before moving it, so it can go back.
    opener.current = document.activeElement;
    heading.current?.focus();

    return () => {
      const returnTo = opener.current;
      if (returnTo instanceof HTMLElement && document.contains(returnTo)) {
        returnTo.focus();
      }
    };
  }, []);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        event.stopPropagation();
        onClose();
        return;
      }
      if (event.key !== 'Tab' || !panel.current) {
        return;
      }

      const focusable = Array.from(
        panel.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((element) => element.offsetParent !== null || element === document.activeElement);

      if (focusable.length === 0) {
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;

      // Wrapping at both ends is the trap. Without the backward case, Shift+Tab
      // from the first control leaves the dialog silently.
      if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      } else if (event.shiftKey && (active === first || active === heading.current)) {
        event.preventDefault();
        last.focus();
      }
    }

    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [onClose]);

  return (
    <>
      {/* The scrim is decorative; closing by clicking it is a convenience, and
          every keyboard path to closing goes through Escape or the close button. */}
      <div className="panel-scrim" onClick={onClose} aria-hidden="true" />
      <div
        className="panel"
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby="panel-title"
        aria-describedby={subtitle ? 'panel-subtitle' : undefined}
      >
        <div className="panel__header">
          <div>
            <h2 id="panel-title" ref={heading} tabIndex={-1} className="panel__title">
              {title}
            </h2>
            {subtitle && (
              <p id="panel-subtitle" className="panel__subtitle">
                {subtitle}
              </p>
            )}
          </div>
          <IconButton icon="✕" label="Close request details" onClick={onClose} />
        </div>
        <div className="panel__body">{children}</div>
      </div>
    </>
  );
}
