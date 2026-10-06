import { useParams } from "react-router";
import { ProjectPage } from "~/components/project/ProjectPage";

/** Project page: /p/:project/:tab? (02-ui-spec §5). A new project starts with clean page state. */
export default function ProjectRoute() {
  const { project = "", tab } = useParams();
  return <ProjectPage key={project} project={project} tab={tab} />;
}
