import SessionManager from "../../session";
import { useNavigate } from "react-router-dom";
import { useEffect } from "react";
import { useAppDispatch } from "../../redux/hooks.ts";
import { loadPrefs, startPath } from "../../redux/thunks/fylvo.ts";

export const HomeRedirect = () => {
  const navigate = useNavigate();
  const dispatch = useAppDispatch();
  useEffect(() => {
    if (SessionManager.currentLoginOrNull()) {
      // Fylvo: open the start page when the account chose it.
      dispatch(loadPrefs()).then((prefs) => navigate(startPath(prefs), { replace: true }));
    } else {
      navigate("/session");
    }
  }, []);

  return <div></div>;
};
