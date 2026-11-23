import { Box, Button, Chip, Container, Stack, Typography } from "@mui/material";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { getFileList } from "../../../api/api.ts";
import { FileResponse } from "../../../api/explorer.ts";
import { HomeWidgetType } from "../../../api/fylvo.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { loadTags } from "../../../redux/thunks/fylvo.ts";
import { navigateToPath } from "../../../redux/thunks/filemanager.ts";
import { defaultPath } from "../../../hooks/useNavigation.tsx";
import { tagSearchUri } from "../../FileManager/TreeView/CustomSidebar.tsx";
import { FileManagerIndex } from "../../FileManager/FileManager.tsx";
import StorageSummary from "../../Frame/NavBar/StorageSummary.tsx";
import PageContainer, { PageEmpty } from "../PageContainer.tsx";
import PageHeader from "../PageHeader.tsx";
import { useFavorites } from "./Favorites.tsx";
import FileRows from "./FileRows.tsx";

// Fylvo: the start page, put together from the blocks the account chose (prefs.home).

const recentCount = 12;
// The search walks folder by folder; a larger window, sorted here, finds recent files in subfolders too.
const recentWindow = 200;
const favoritesCount = 8;

const Section = ({
  title,
  action,
  children,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) => (
  <Box sx={{ mb: 3 }}>
    <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 1, minHeight: 28 }}>
      <Typography variant="body2" fontWeight={600} color="text.secondary">
        {title}
      </Typography>
      {action}
    </Stack>
    {children}
  </Box>
);

const Recent = () => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const [files, setFiles] = useState<FileResponse[] | undefined>();
  useEffect(() => {
    // Like the phone app: the newest files of the whole account.
    dispatch(
      getFileList({
        uri: `${defaultPath}?type=file`,
        page_size: recentWindow,
        order_by: "updated_at",
        order_direction: "desc",
      }),
    )
      .then((res) =>
        setFiles(
          [...res.files]
            .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
            .slice(0, recentCount),
        ),
      )
      .catch(() => setFiles([]));
  }, []);
  return (
    <Section title={t("customize.widgetRecent")}>
      {files && files.length > 0 && <FileRows files={files} />}
      {files && files.length == 0 && <PageEmpty>{t("customize.noRecent")}</PageEmpty>}
    </Section>
  );
};

const FavoritesBlock = () => {
  const { t } = useTranslation("application");
  const navigate = useNavigate();
  const { files } = useFavorites();
  return (
    <Section
      title={t("customize.favorites")}
      action={
        files && files.length > favoritesCount ? (
          <Button size="small" onClick={() => navigate("/favorites")}>
            {t("customize.showAll")}
          </Button>
        ) : undefined
      }
    >
      {files && files.length > 0 && <FileRows files={files.slice(0, favoritesCount)} />}
      {files && files.length == 0 && <PageEmpty>{t("customize.noFavorites")}</PageEmpty>}
    </Section>
  );
};

const LinksBlock = () => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const links = useAppSelector((state) => state.fylvo.prefs?.sidebar.items.filter((item) => item.type == "link"));
  if (!links?.length) {
    return null;
  }
  return (
    <Section title={t("customize.widgetLinks")}>
      <Stack direction="row" sx={{ flexWrap: "wrap", gap: 1 }}>
        {links.map((link) => (
          <Chip
            key={link.uri}
            label={link.name || link.uri}
            onClick={() => link.uri && dispatch(navigateToPath(FileManagerIndex.main, link.uri))}
          />
        ))}
      </Stack>
    </Section>
  );
};

const TagsBlock = () => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const tags = useAppSelector((state) => state.fylvo.tags);
  useEffect(() => {
    dispatch(loadTags());
  }, []);
  if (!tags?.length) {
    return null;
  }
  return (
    <Section title={t("customize.tags")}>
      <Stack direction="row" sx={{ flexWrap: "wrap", gap: 1 }}>
        {tags.map((tag) => (
          <Chip
            key={tag.name}
            label={`${tag.name} · ${tag.count}`}
            icon={
              <Box
                sx={{
                  width: 9,
                  height: 9,
                  borderRadius: "50%",
                  ml: "8px !important",
                  backgroundColor: tag.color || "text.secondary",
                }}
              />
            }
            onClick={() => dispatch(navigateToPath(FileManagerIndex.main, tagSearchUri(tag.name)))}
          />
        ))}
      </Stack>
    </Section>
  );
};

// The storage summary has its own title.
const StorageBlock = () => (
  <Box sx={{ mb: 3, maxWidth: 360 }}>
    <StorageSummary />
  </Box>
);

const blocks: Record<HomeWidgetType, () => JSX.Element | null> = {
  recent: Recent,
  favorites: FavoritesBlock,
  links: LinksBlock,
  tags: TagsBlock,
  storage: StorageBlock,
};

const StartPage = () => {
  const { t } = useTranslation("application");
  const navigate = useNavigate();
  const widgets = useAppSelector((state) => state.fylvo.prefs?.home.widgets);

  return (
    <PageContainer compact>
      <Container maxWidth="lg">
        <PageHeader
          title={t("customize.start")}
          secondaryAction={
            <Button size="small" onClick={() => navigate("/settings?tab=customize")}>
              {t("customize.customize")}
            </Button>
          }
        />
        {widgets
          ?.filter((w) => !w.hidden)
          .map((w) => {
            const Block = blocks[w.type];
            return Block ? <Block key={w.type} /> : null;
          })}
      </Container>
    </PageContainer>
  );
};

export default StartPage;
