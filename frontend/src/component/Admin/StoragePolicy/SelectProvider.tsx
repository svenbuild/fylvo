import {
  Card,
  CardActionArea,
  CardContent,
  CardMedia,
  DialogContent,
  Grid2,
  styled,
  Typography,
} from "@mui/material";
import { hoverSurface } from "../../../fylvoTheme.ts";
import { useTranslation } from "react-i18next";
import { PolicyType } from "../../../api/explorer";
import DraggableDialog from "../../Dialogs/DraggableDialog";
import { PolicyPropsMap } from "./StoragePolicySetting";

export interface SelectProviderProps {
  open: boolean;
  onClose: () => void;
  onSelect: (provider: PolicyType) => void;
}

const StyledCard = styled(Card)(({ theme }) => ({
  display: "flex",
  "&:hover": { backgroundColor: hoverSurface(theme) },
  "&:focus-within": { borderColor: theme.palette.primary.main },
}));

const SelectProvider = ({ open, onClose, onSelect }: SelectProviderProps) => {
  const { t } = useTranslation("dashboard");
  return (
    <DraggableDialog
      title={t("policy.selectAStorageProvider")}
      dialogProps={{
        open,
        onClose,
        fullWidth: true,
        maxWidth: "sm",
      }}
    >
      <DialogContent dividers>
        <Grid2 container spacing={1} sx={{ py: 1 }}>
          {Object.values(PolicyType)
            .filter((type) => type != PolicyType.load_balance)
            .map((type) => (
            <Grid2 key={type.toString()} size={{ xs: 12, sm: 6 }}>
              <StyledCard>
                <CardActionArea
                  sx={{ display: "flex", justifyContent: "flex-start", minHeight: 40, px: 1, gap: 1 }}
                  onClick={() => onSelect(type)}
                >
                  <CardMedia
                    component="img"
                    image={PolicyPropsMap[type].img}
                    sx={{ width: 44, height: 32, objectFit: "contain" }}
                  />
                  <CardContent sx={{ p: 0, "&:last-child": { pb: 0 } }}>
                    <Typography variant="body2" color="text.primary">
                      {t(PolicyPropsMap[type].name)}
                    </Typography>
                  </CardContent>
                </CardActionArea>
              </StyledCard>
            </Grid2>
          ))}
        </Grid2>
      </DialogContent>
    </DraggableDialog>
  );
};

export default SelectProvider;
