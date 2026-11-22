import { ArrowDownward, ArrowUpward, DragIndicator } from "@mui/icons-material";
import { Box, DialogContent, IconButton, Menu, MenuItem, Stack, Tooltip, Typography } from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SidebarItem } from "../../../api/fylvo.ts";
import { raisedHoverSurface } from "../../../fylvoTheme.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { selectPath } from "../../../redux/thunks/dialog.ts";
import { loadPrefs, loadTags, savePrefs } from "../../../redux/thunks/fylvo.ts";
import { FilledTextField, SecondaryButton } from "../../Common/StyledComponents.tsx";
import DraggableDialog from "../../Dialogs/DraggableDialog.tsx";
import Dismiss from "../../Icons/Dismiss.tsx";
import Eye from "../../Icons/Eye.tsx";
import EyeOff from "../../Icons/EyeOff.tsx";
import { tagSearchUri } from "./CustomSidebar.tsx";

// Fylvo: the dialog to arrange the sidebar. Entries can be dragged, hidden, renamed (links) and
// removed (links, headings); folders, tags and headings can be added.

const categoryLabels: Record<string, string> = {
  image: "navbar.photos",
  video: "navbar.videos",
  audio: "navbar.music",
  document: "navbar.documents",
};

export const useSidebarLabel = () => {
  const { t } = useTranslation("application");
  return (item: SidebarItem): string => {
    switch (item.type) {
      case "home":
        return t("customize.start");
      case "my_files":
        return t("navbar.myFiles");
      case "favorites":
        return t("customize.favorites");
      case "tags":
        return t("customize.tags");
      case "shared_with_me":
        return t("navbar.sharedWithMe");
      case "trash":
        return t("navbar.trash");
      case "category":
        return t(categoryLabels[item.category ?? ""] ?? "");
      case "connected":
        return t("customize.connected");
      default:
        return item.name || item.uri || "";
    }
  };
};

export interface CustomizeSidebarDialogProps {
  open: boolean;
  onClose: () => void;
}

const CustomizeSidebarDialog = ({ open, onClose }: CustomizeSidebarDialogProps) => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const label = useSidebarLabel();
  const prefs = useAppSelector((state) => state.fylvo.prefs);
  const tags = useAppSelector((state) => state.fylvo.tags);
  // Each row keeps its own key, so editing a name or moving a row does not remount it.
  const [rows, setRows] = useState<{ key: number; item: SidebarItem }[]>([]);
  const nextKey = useRef(0);
  const toRows = (list: SidebarItem[]) => list.map((item) => ({ key: nextKey.current++, item }));
  const items = rows.map((r) => r.item);
  const [dragging, setDragging] = useState<number | undefined>();
  const [heading, setHeading] = useState("");
  const [tagMenu, setTagMenu] = useState<HTMLElement | null>(null);
  const [loading, setLoading] = useState(false);

  // The rows come from freshly loaded preferences, so a save never drops a link pinned
  // meanwhile on another device. Saving waits for them.
  const [fresh, setFresh] = useState(false);
  useEffect(() => {
    if (!open) {
      return;
    }
    setFresh(false);
    if (prefs) {
      setRows(toRows(prefs.sidebar.items));
    }
    dispatch(loadTags());
    dispatch(loadPrefs()).then((loaded) => {
      if (loaded) {
        setRows(toRows(loaded.sidebar.items));
        setFresh(true);
      }
    });
  }, [open]);

  const move = (from: number, to: number) => {
    if (from == to || to < 0 || to >= rows.length) return;
    setRows((current) => {
      const next = [...current];
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      return next;
    });
  };

  const update = (index: number, change: Partial<SidebarItem>) =>
    setRows((current) => current.map((r, i) => (i == index ? { ...r, item: { ...r.item, ...change } } : r)));

  const remove = (index: number) => setRows((current) => current.filter((_, i) => i != index));

  const add = (item: SidebarItem) => setRows((current) => [...current, { key: nextKey.current++, item }]);

  const addLink = (item: SidebarItem) => {
    if (!items.some((i) => i.type == "link" && i.uri == item.uri)) {
      add(item);
    }
  };

  const addFolder = async () => {
    try {
      const path = await dispatch(selectPath("sidebarLink"));
      addLink({ type: "link", uri: path });
    } catch (e) {}
  };

  const addHeading = () => {
    if (heading.trim()) {
      add({ type: "heading", name: heading.trim() });
      setHeading("");
    }
  };

  const save = async () => {
    setLoading(true);
    try {
      await dispatch(savePrefs({ sidebar: { items } }));
      onClose();
    } catch (e) {
    } finally {
      setLoading(false);
    }
  };

  // Only the sidebar goes back to the default (the links stay); it is saved at once.
  const reset = async () => {
    setLoading(true);
    try {
      await dispatch(savePrefs({ sidebar: null }));
      onClose();
    } catch (e) {
    } finally {
      setLoading(false);
    }
  };

  return (
    <DraggableDialog
      title={t("customize.editSidebar")}
      showActions
      showCancel
      loading={loading}
      disabled={!fresh}
      onAccept={save}
      okText={t("customize.save")}
      secondaryAction={
        <SecondaryButton variant="contained" onClick={reset} disabled={loading}>
          {t("customize.reset")}
        </SecondaryButton>
      }
      dialogProps={{ open, onClose, fullWidth: true, maxWidth: "xs" }}
    >
      <DialogContent sx={{ pt: 1 }}>
        <Typography variant="caption" color="text.secondary" sx={{ display: "block", mb: 1 }}>
          {t("customize.editSidebarHint")}
        </Typography>
        <Stack spacing={0.25}>
          {rows.map(({ key, item }, index) => {
            const removable = item.type == "link" || item.type == "heading";
            return (
              <Box
                key={key}
                draggable
                onDragStart={() => setDragging(index)}
                onDragOver={(e) => {
                  e.preventDefault();
                  if (dragging !== undefined && dragging != index) {
                    move(dragging, index);
                    setDragging(index);
                  }
                }}
                onDragEnd={() => setDragging(undefined)}
                sx={{
                  display: "flex",
                  alignItems: "center",
                  gap: 1,
                  minHeight: 32,
                  px: 0.5,
                  borderRadius: "6px",
                  cursor: "grab",
                  opacity: item.hidden ? 0.5 : dragging == index ? 0.6 : 1,
                  "&:hover": { backgroundColor: (theme) => raisedHoverSurface(theme) },
                }}
              >
                <DragIndicator fontSize="small" sx={{ color: "text.secondary" }} />
                {item.type == "link" || item.type == "heading" ? (
                  <FilledTextField
                    variant="filled"
                    size="small"
                    hiddenLabel
                    value={item.name ?? ""}
                    placeholder={item.type == "link" ? item.uri : ""}
                    onChange={(e) => update(index, { name: e.target.value })}
                    inputProps={{ maxLength: 64 }}
                    sx={{ flexGrow: 1, "& input": { py: "4px", fontWeight: item.type == "heading" ? 600 : 400 } }}
                  />
                ) : (
                  <Typography variant="body2" sx={{ flexGrow: 1 }}>
                    {label(item)}
                  </Typography>
                )}
                <IconButton size="small" disabled={index == 0} onClick={() => move(index, index - 1)}>
                  <ArrowUpward sx={{ fontSize: 16 }} />
                </IconButton>
                <IconButton size="small" disabled={index == rows.length - 1} onClick={() => move(index, index + 1)}>
                  <ArrowDownward sx={{ fontSize: 16 }} />
                </IconButton>
                {removable ? (
                  <Tooltip title={t("customize.remove")}>
                    <IconButton size="small" onClick={() => remove(index)}>
                      <Dismiss fontSize="small" />
                    </IconButton>
                  </Tooltip>
                ) : (
                  <Tooltip title={item.hidden ? t("customize.show") : t("customize.hide")}>
                    <IconButton size="small" onClick={() => update(index, { hidden: !item.hidden })}>
                      {item.hidden ? <EyeOff fontSize="small" /> : <Eye fontSize="small" />}
                    </IconButton>
                  </Tooltip>
                )}
              </Box>
            );
          })}
        </Stack>
        <Stack direction="row" spacing={1} sx={{ mt: 2, flexWrap: "wrap", rowGap: 1 }}>
          <SecondaryButton variant="contained" size="small" onClick={addFolder}>
            {t("customize.addFolder")}
          </SecondaryButton>
          <SecondaryButton
            variant="contained"
            size="small"
            disabled={!tags?.length}
            onClick={(e) => setTagMenu(e.currentTarget)}
          >
            {t("customize.addTag")}
          </SecondaryButton>
          <Menu anchorEl={tagMenu} open={!!tagMenu} onClose={() => setTagMenu(null)}>
            {tags?.map((tag) => (
              <MenuItem
                key={tag.name}
                dense
                onClick={() => {
                  addLink({ type: "link", uri: tagSearchUri(tag.name), name: tag.name });
                  setTagMenu(null);
                }}
              >
                {tag.name}
              </MenuItem>
            ))}
          </Menu>
        </Stack>
        <Stack direction="row" spacing={1} sx={{ mt: 1 }}>
          <FilledTextField
            variant="filled"
            size="small"
            hiddenLabel
            placeholder={t("customize.headingName")}
            value={heading}
            onChange={(e) => setHeading(e.target.value)}
            onKeyDown={(e) => e.key == "Enter" && addHeading()}
            sx={{ flexGrow: 1, "& input": { py: "6px" } }}
            inputProps={{ maxLength: 64 }}
          />
          <SecondaryButton variant="contained" size="small" onClick={addHeading} disabled={!heading.trim()}>
            {t("customize.addHeading")}
          </SecondaryButton>
        </Stack>
      </DialogContent>
    </DraggableDialog>
  );
};

export default CustomizeSidebarDialog;
