import { Box } from "@mui/material";
import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import {
  DesktopScheme,
  setDesktopMountSetupDialog,
  setHeadlessFrameLoading,
} from "../../../../redux/globalStateSlice.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { openDesktopCallback } from "../../../../redux/thunks/session.ts";
import { useQuery } from "../../../../util/index.ts";

// Landing page after the OAuth consent of a desktop app. It hands the code back to the app
// through its deep link scheme.
const DesktopCallback = ({ scheme }: { scheme: DesktopScheme }) => {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();
  const query = useQuery();

  useEffect(() => {
    dispatch(setHeadlessFrameLoading(true));
    const code = query.get("code");
    const state = query.get("state");
    navigate("/home");
    if (code) {
      if (state && state.startsWith("reauthorize:")) {
        dispatch(openDesktopCallback(scheme, code, state));
      } else {
        dispatch(setDesktopMountSetupDialog({ open: true, state: { code, state: state ?? "", scheme } }));
      }
    }
  }, []);

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        pt: 7,
        pb: 9,
      }}
    ></Box>
  );
};

export default DesktopCallback;
