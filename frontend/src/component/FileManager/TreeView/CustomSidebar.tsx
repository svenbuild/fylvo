import { SpaceDashboardOutlined } from "@mui/icons-material";
import { Box, Typography } from "@mui/material";
import { memo, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import { SidebarItem } from "../../../api/fylvo.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { loadTags } from "../../../redux/thunks/fylvo.ts";
import { navigateToPath } from "../../../redux/thunks/filemanager.ts";
import { defaultPath, defaultSharedWithMePath, defaultTrashPath } from "../../../hooks/useNavigation.tsx";
import CrUri, { Filesystem, UriQuery } from "../../../util/uri.ts";
import SideNavItem from "../../Frame/NavBar/SideNavItem.tsx";
import StarFilled from "../../Icons/StarFilled.tsx";
import { Metadata } from "../../../api/explorer.ts";
import { FileManagerIndex } from "../FileManager.tsx";
import TreeFiles from "./TreeFiles.tsx";
import { connectedRootUri } from "../../../api/connected.ts";

// Fylvo: the sidebar as the account arranged it (prefs.sidebar). Rendered inside the tree of the
// main file manager; the folder tree under "My files" stays.

export const tagSearchUri = (name: string) => {
  const uri = new CrUri(defaultPath);
  uri.addQuery(UriQuery.metadata_prefix + Metadata.tag_prefix + name, "");
  return uri.toString();
};

export const categoryPath = (category: string) => `${defaultPath}/?category=${category}`;

const SectionLabel = ({ label }: { label: string }) => (
  <Typography
    variant="caption"
    sx={{ display: "block", pl: "24px", pr: "6px", pt: "10px", pb: "2px", color: "text.secondary", fontWeight: 600 }}
  >
    {label}
  </Typography>
);

const TagDot = ({ color }: { color?: string }) => (
  <Box
    sx={{
      width: 9,
      height: 9,
      borderRadius: "50%",
      ml: "3px",
      backgroundColor: color || "transparent",
      border: (theme) => (color ? "none" : `1px solid ${theme.palette.text.secondary}`),
    }}
  />
);

export interface CustomSidebarProps {
  items: SidebarItem[];
  elements?: string[];
  currentFs?: string;
  pathRoot?: string;
  disableSharedWithMe?: boolean;
  disableTrash?: boolean;
}

const CustomSidebar = memo(
  ({ items, elements, currentFs, pathRoot, disableSharedWithMe, disableTrash }: CustomSidebarProps) => {
    const { t } = useTranslation("application");
    const dispatch = useAppDispatch();
    const navigate = useNavigate();
    const location = useLocation();
    const favorites = useAppSelector((state) => state.fylvo.prefs?.favorites);
    const tags = useAppSelector((state) => state.fylvo.tags);

    const showTags = items.some((item) => item.type == "tags" && !item.hidden);
    useEffect(() => {
      if (showTags) {
        dispatch(loadTags());
      }
    }, [showTags]);

    const render = (item: SidebarItem, index: number) => {
      if (item.hidden) {
        return null;
      }
      switch (item.type) {
        case "home":
          return (
            <SideNavItem
              key="home"
              level={0}
              active={location.pathname == "/start"}
              icon={<SpaceDashboardOutlined sx={{ fontSize: 16 }} />}
              label={t("customize.start")}
              onClick={() => navigate("/start")}
            />
          );
        case "my_files":
          return (
            <TreeFiles
              canDrop
              level={0}
              path={defaultPath}
              key={defaultPath}
              elements={currentFs == Filesystem.my ? elements : undefined}
            />
          );
        case "favorites":
          if (!favorites?.length) {
            return null;
          }
          return (
            <SideNavItem
              key="favorites"
              level={0}
              active={location.pathname == "/favorites"}
              icon={<StarFilled fontSize="small" />}
              label={t("customize.favorites")}
              onClick={() => navigate("/favorites")}
            />
          );
        case "tags":
          if (!tags?.length) {
            return null;
          }
          return (
            <Box key="tags">
              <SectionLabel label={t("customize.tags")} />
              {tags.map((tag) => (
                <SideNavItem
                  key={"tag" + tag.name}
                  level={0}
                  icon={<TagDot color={tag.color} />}
                  label={
                    <Box component="span" sx={{ display: "flex", gap: 1, justifyContent: "space-between" }}>
                      <span>{tag.name}</span>
                      <Box component="span" sx={{ color: "text.secondary" }}>
                        {tag.count}
                      </Box>
                    </Box>
                  }
                  onClick={() => dispatch(navigateToPath(FileManagerIndex.main, tagSearchUri(tag.name)))}
                />
              ))}
            </Box>
          );
        case "category":
          return (
            <TreeFiles flatten level={0} path={categoryPath(item.category ?? "")} key={"category" + item.category} />
          );
        case "shared_with_me":
          return disableSharedWithMe ? null : (
            <TreeFiles level={0} path={defaultSharedWithMePath} key={defaultSharedWithMePath} />
          );
        case "trash":
          return disableTrash ? null : (
            <TreeFiles
              level={0}
              flatten
              canDrop
              key={defaultTrashPath}
              path={defaultTrashPath}
              elements={currentFs == Filesystem.trash ? elements : undefined}
            />
          );
        case "link": {
          if (!item.uri) {
            return null;
          }
          let uri: CrUri;
          try {
            uri = new CrUri(item.uri);
          } catch (e) {
            return null;
          }
          return (
            <TreeFiles
              flatten={uri.fs() != Filesystem.share || !uri.is_root() || uri.is_search()}
              canDrop={(uri.fs() == Filesystem.share || uri.fs() == Filesystem.my) && !uri.is_search()}
              level={0}
              labelOverwrite={item.name}
              path={item.uri}
              elements={item.uri == pathRoot ? elements : undefined}
              key={"Pined" + item.uri}
              pinned
            />
          );
        }
        case "heading":
          return <SectionLabel key={"heading" + index} label={item.name ?? ""} />;
        case "connected": {
          // The server keeps these entries in line with the connections of the account.
          if (!item.id) {
            return null;
          }
          const root = connectedRootUri(item.id);
          return (
            <TreeFiles
              level={0}
              path={root}
              key={"connected" + item.id}
              elements={root == pathRoot ? elements : undefined}
            />
          );
        }
        default:
          return null;
      }
    };

    return <>{items.map(render)}</>;
  },
);

export default CustomSidebar;
