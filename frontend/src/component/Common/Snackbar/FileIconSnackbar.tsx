import { Box } from "@mui/material";
import MuiSnackbarContent from "@mui/material/SnackbarContent";
import { CustomContentProps } from "notistack";
import * as React from "react";
import { forwardRef, useState } from "react";
import { FileResponse } from "../../../api/explorer.ts";
import FileTypeIcon from "../../FileManager/Explorer/FileTypeIcon.tsx";

declare module "notistack" {
  interface VariantOverrides {
    file: {
      file: FileResponse;
    };
  }
}

interface FileIconSnackbarProps extends CustomContentProps {
  file: FileResponse;
}

const FileIconSnackbar = forwardRef<HTMLDivElement, FileIconSnackbarProps>((props, ref) => {
  const [progress, setProgress] = useState(0);
  const {
    // You have access to notistack props and options 👇🏼
    message,
    action,
    id,
    file,
    // as well as your own custom props 👇🏼
    ...other
  } = props;

  let componentOrFunctionAction: React.ReactNode = undefined;
  if (typeof action === "function") {
    componentOrFunctionAction = action(id);
  } else {
    componentOrFunctionAction = action;
  }

  return (
    <MuiSnackbarContent
      ref={ref}
      sx={{
        bgcolor: "background.paper",
        color: "text.primary",
        border: 1,
        borderColor: "divider",
        boxShadow: "none",
        width: "100%",
        px: "10px",
        py: 0,
        "& .MuiSnackbarContent-message": { width: "100%", py: "6px" },
      }}
      message={
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            width: "100%",
          }}
        >
          <Box sx={{ display: "flex", alignItems: "center", flexGrow: 1, gap: 1 }}>
            <FileTypeIcon sx={{ width: 16, height: 16 }} name={file.name} fileType={file.type} />
            <Box>{message}</Box>
          </Box>

          {componentOrFunctionAction && (
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                marginLeft: "auto",
                paddingLeft: "16px",
                marginRight: "-8px",
              }}
            >
              {componentOrFunctionAction}
            </Box>
          )}
        </Box>
      }
    />
  );
});

export default FileIconSnackbar;
