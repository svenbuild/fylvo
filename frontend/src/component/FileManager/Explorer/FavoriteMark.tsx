import { Tooltip } from "@mui/material";
import { useTranslation } from "react-i18next";
import { FileResponse } from "../../../api/explorer.ts";
import { useAppSelector } from "../../../redux/hooks.ts";
import StarFilled from "../../Icons/StarFilled.tsx";

// Fylvo: a small star next to the name of a favorite.
const FavoriteMark = ({ file }: { file: FileResponse }) => {
  const { t } = useTranslation("application");
  const favorite = useAppSelector((state) => !!state.fylvo.prefs?.favorites.includes(file.id));
  if (!favorite) {
    return null;
  }
  return (
    <Tooltip title={t("customize.favorite")}>
      <StarFilled sx={{ fontSize: 12, flexShrink: 0, color: "text.secondary" }} />
    </Tooltip>
  );
};

export default FavoriteMark;
