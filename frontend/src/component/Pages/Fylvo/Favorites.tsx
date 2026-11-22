import { Container, Typography } from "@mui/material";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse } from "../../../api/explorer.ts";
import { getFavorites } from "../../../api/fylvo.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { setFavorite } from "../../../redux/thunks/fylvo.ts";
import PageContainer, { PageEmpty } from "../PageContainer.tsx";
import PageHeader from "../PageHeader.tsx";
import FileRows from "./FileRows.tsx";

// Fylvo: the favorites of the account, newest first.

export const useFavorites = () => {
  const dispatch = useAppDispatch();
  const generation = useAppSelector((state) => state.fylvo.favoritesGeneration);
  const [files, setFiles] = useState<FileResponse[] | undefined>();
  const [unavailable, setUnavailable] = useState(0);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(() => {
    setLoading(true);
    dispatch(getFavorites())
      .then((res) => {
        setFiles(res.files);
        setUnavailable(res.unavailable);
      })
      .catch(() => setFiles([]))
      .finally(() => setLoading(false));
  }, [dispatch]);

  useEffect(refresh, [generation]);
  return { files, unavailable, loading, refresh };
};

const Favorites = () => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const { files, unavailable, loading, refresh } = useFavorites();

  return (
    <PageContainer compact>
      <Container maxWidth="lg">
        <PageHeader title={t("customize.favorites")} onRefresh={refresh} loading={loading} />
        {files && files.length > 0 && (
          <FileRows files={files} onUnstar={(file) => dispatch(setFavorite([file], false))} />
        )}
        {files && files.length == 0 && <PageEmpty>{t("customize.noFavorites")}</PageEmpty>}
        {unavailable > 0 && (
          <Typography variant="caption" color="text.secondary" component="div" sx={{ mt: 1 }}>
            {t("customize.favoritesUnavailable", { count: unavailable })}
          </Typography>
        )}
      </Container>
    </PageContainer>
  );
};

export default Favorites;
