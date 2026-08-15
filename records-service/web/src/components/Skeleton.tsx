/**
 * Loading placeholders.
 *
 * Every region that will hold data shows the shape of that data while it is
 * being fetched, rather than a spinner or an empty page. A spinner says
 * "something is happening"; a skeleton says "a table of five columns is
 * arriving here", which is the difference between waiting and wondering whether
 * the page is broken. The placeholders are hidden from assistive technology --
 * a screen reader user gets the live-region announcement instead, which is
 * information rather than decoration.
 */

/** A single shimmering bar, sized as a fraction of its column. */
export function SkeletonBar({ width = '100%' }: { width?: string }) {
  return <span className="skeleton" style={{ width }} aria-hidden="true" />;
}

/** Placeholder rows for the request table. */
export function SkeletonRows({ rows = 4 }: { rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, index) => (
        <tr key={index} className="skeleton-row" aria-hidden="true">
          <td><SkeletonBar width="80%" /></td>
          <td><SkeletonBar width="60%" /></td>
          <td><SkeletonBar width="50%" /></td>
          <td><SkeletonBar width="45%" /></td>
          <td><SkeletonBar width="30%" /></td>
        </tr>
      ))}
    </>
  );
}

/** Placeholder lines for the detail panel. */
export function SkeletonDetail() {
  return (
    <div className="skeleton-detail" aria-hidden="true">
      <SkeletonBar width="70%" />
      <SkeletonBar width="90%" />
      <SkeletonBar width="55%" />
      <SkeletonBar width="80%" />
    </div>
  );
}
