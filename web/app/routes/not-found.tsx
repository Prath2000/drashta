import { Link } from "react-router";

export default function NotFound() {
  return (
    <div className="dr-placeholder">
      <div className="dr-cartouche">
        <span className="dr-head">Nothing here</span>
        <span className="dr-empty">
          no screen at this address. <Link to="/">back to the seismograph</Link>
        </span>
      </div>
    </div>
  );
}
