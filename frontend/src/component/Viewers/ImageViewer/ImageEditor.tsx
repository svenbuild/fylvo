import { alpha, Backdrop, Box, Theme, useTheme } from "@mui/material";
import i18next from "i18next";
import { useSnackbar } from "notistack";
import React, { useCallback, useEffect, useState } from "react";
import FilerobotImageEditor from "react-filerobot-image-editor";
import { useTranslation } from "react-i18next";
import { getFileEntityUrl } from "../../../api/api.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { saveImage, switchToImageViewer } from "../../../redux/thunks/viewer.ts";
import { fileExtension, getFileLinkedUri } from "../../../util";
import { hoverSurface, raisedHoverSurface, selectedSurface, shellSurface } from "../../../fylvoTheme.ts";
import "./editor.css";

// The editor brings its own light colors. Map them to the Fylvo theme so it follows dark and light mode.
const editorTheme = (theme: Theme) => {
  const { primary, text, divider, background } = theme.palette;
  return {
    palette: {
      "txt-primary": text.primary,
      "txt-secondary": text.secondary,
      "txt-placeholder": text.disabled,
      "accent-primary": primary.main,
      "accent-primary-hover": primary.dark,
      "accent-primary-active": primary.main,
      "accent-primary-disabled": selectedSurface(theme),
      "accent-stateless": primary.main,
      accent_0_5_opacity: alpha(primary.main, 0.05),
      accent_1_2_opacity: alpha(primary.main, 0.12),
      accent_1_8_opacity: alpha(primary.main, 0.18),
      accent_2_8_opacity: alpha(primary.main, 0.28),
      "bg-secondary": shellSurface(theme),
      "bg-primary": background.paper,
      "bg-stateless": background.paper,
      "bg-hover": raisedHoverSurface(theme),
      "bg-primary-hover": hoverSurface(theme),
      "bg-active": selectedSurface(theme),
      "bg-primary-active": selectedSurface(theme),
      "icon-primary": text.primary,
      "icons-primary": text.primary,
      "icons-secondary": text.secondary,
      "icons-muted": text.disabled,
      "icons-placeholder": text.disabled,
      "btn-primary-text": primary.contrastText,
      "btn-disabled-text": text.disabled,
      "link-primary": primary.main,
      "borders-primary": divider,
      "borders-secondary": divider,
      "borders-item": divider,
      "border-primary-stateless": divider,
      "borders-strong": alpha(text.primary, 0.3),
      "light-shadow": "rgba(0, 0, 0, 0.2)",
    },
    typography: { fontFamily: theme.typography.fontFamily },
  };
};

export const editorSupportedExt = ["jpg", "jpeg", "png", "webp"];

const ImageEditor = () => {
  const theme = useTheme();
  const { t, i18n } = useTranslation();
  const [nsLoaded, setNsLoaded] = useState(false);

  useEffect(() => {
    i18next.loadNamespaces(["image_editor"]).then(() => {
      setNsLoaded(true);
    });
  }, []);

  const editorState = useAppSelector((state) => state.globalState.imageEditor);
  const [imageSrc, setImageSrc] = React.useState<string | undefined>(undefined);
  useEffect(() => {
    if (!editorState?.open) {
      setImageSrc(undefined);
    } else {
      dispatch(
        getFileEntityUrl({
          no_cache: true,
          uris: [getFileLinkedUri(editorState.file)],
          entity: editorState.version,
        }),
      )
        .then((res) => {
          setImageSrc(res.urls[0].url);
        })
        .catch(() => {
          dispatch(switchToImageViewer());
        });
    }
  }, [editorState]);

  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();

  const save = useCallback(
    async (name: string, data?: string) => {
      if (!data || !editorState?.file) {
        return;
      }

      await dispatch(saveImage(name, data, editorState.file, editorState.version));
      dispatch(switchToImageViewer());
    },
    [dispatch, editorState],
  );

  return (
    <>
      <Backdrop
        sx={{
          zIndex: (theme) => theme.zIndex.drawer + 1,
        }}
        open={true}
      >
        <Box sx={{ width: "100%", height: "100%", "--fylvo-editor-frame": shellSurface }}>
          {editorState && imageSrc && nsLoaded && (
            <FilerobotImageEditor
              translations={i18n.getResourceBundle(i18n.language, "image_editor")}
              useBackendTranslations={false}
              source={imageSrc}
              onSave={async (editedImageObject, _designState) => {
                await save(editedImageObject.name, editedImageObject.imageBase64);
              }}
              defaultSavedImageName={editorState.file.name}
              // @ts-ignore
              defaultSavedImageType={fileExtension(editorState.file.name) ?? "png"}
              onClose={() => dispatch(switchToImageViewer())}
              disableSaveIfNoChanges={true}
              previewPixelRatio={window.devicePixelRatio}
              theme={editorTheme(theme)}
              savingPixelRatio={4}
            />
          )}
        </Box>
      </Backdrop>
    </>
  );
};

export default ImageEditor;
