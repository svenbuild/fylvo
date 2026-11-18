import { Container } from "@mui/material";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useParams } from "react-router-dom";
import AdminPage from "../../Common/AdminPage.tsx";
import PageHeader from "../../../Pages/PageHeader";
import GroupForm from "./GroupForm";
import GroupSettingWrapper from "./GroupSettingWrapper";

const EditGroup = () => {
  const { t } = useTranslation("dashboard");
  const { id } = useParams();
  const [name, setName] = useState("");

  return (
    <AdminPage>
      <Container maxWidth="xl">
        <PageHeader title={t("group.editGroup", { group: name })} />
        <GroupSettingWrapper groupID={parseInt(id ?? "1") ?? 1} onGroupChange={(p) => setName(p.name)}>
          <GroupForm />
        </GroupSettingWrapper>
      </Container>
    </AdminPage>
  );
};

export default EditGroup;
