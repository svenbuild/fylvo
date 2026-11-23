import { ChevronLeft } from "@mui/icons-material";
import { Box, IconButton, styled, Tooltip, useMediaQuery, useTheme } from "@mui/material";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { setDrawerOpen } from "../../../redux/globalStateSlice.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import Logo from "../../Common/Logo.tsx";
import CustomizeSidebarDialog from "../../FileManager/TreeView/CustomizeSidebarDialog.tsx";
import Edit from "../../Icons/Edit.tsx";

export const DrawerHeaderContainer = styled("div")(({ theme }) => ({
  display: "flex",
  alignItems: "center",
  padding: "0 8px 0 12px",
  minHeight: 44,
  flexShrink: 0,
  [theme.breakpoints.down("sm")]: { minHeight: 48 },
  "@media (pointer: coarse)": { minHeight: 48 },
  justifyContent: "flex-end",
}));

// customize (Fylvo): a small pencil that opens the dialog for arranging the sidebar. Like the
// button that closes the drawer it only shows while the pointer is over the header.
const DrawerHeader = ({ disabled, customize }: { disabled?: boolean; customize?: boolean }) => {
  const { t } = useTranslation("application");
  const [customizeOpen, setCustomizeOpen] = useState(false);
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const dispatch = useAppDispatch();
  const navigate = useNavigate();

  return (
    <DrawerHeaderContainer
      sx={{
        "& .MuiIconButton-root": { opacity: 0 },
        ...(!disabled && {
          "&:hover .MuiIconButton-root, & .MuiIconButton-root.Mui-focusVisible": { opacity: 1 },
          "@media (pointer: coarse)": { "& .MuiIconButton-root": { opacity: 1, minWidth: 40, minHeight: 40 } },
        }),
      }}
    >
      <Box sx={{ flexGrow: 1, minWidth: 0, cursor: "pointer" }} onClick={() => navigate("/home")}>
        <Logo
          sx={{
            height: "auto",
            maxWidth: 108,
            maxHeight: 24,
            width: "100%",
            objectPosition: "left",
            objectFit: "contain",
          }}
        />
      </Box>
      {customize && (
        <Tooltip title={t("customize.editSidebar")}>
          <IconButton
            size="small"
            aria-label={t("customize.editSidebar")}
            onClick={() => setCustomizeOpen(true)}
            sx={{
              color: "text.secondary",
              "&.Mui-focusVisible": { outline: "1px solid", outlineColor: "primary.main", outlineOffset: -1 },
            }}
          >
            <Edit sx={{ fontSize: 16 }} />
          </IconButton>
        </Tooltip>
      )}
      {customize && <CustomizeSidebarDialog open={customizeOpen} onClose={() => setCustomizeOpen(false)} />}
      {!isMobile && (
        <IconButton
          aria-label="close drawer"
          onClick={() => dispatch(setDrawerOpen(false))}
          sx={{
            color: "text.secondary",
            "&.Mui-focusVisible": { outline: "1px solid", outlineColor: "primary.main", outlineOffset: -1 },
          }}
        >
          <ChevronLeft />
        </IconButton>
      )}
    </DrawerHeaderContainer>
  );
};

export default DrawerHeader;
