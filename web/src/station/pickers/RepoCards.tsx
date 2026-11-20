import type { ReactNode } from "react";

import type { RunnerRepo } from "../../api";
import { useFolderArt } from "../../game/warroom/castArt";
import { useI18n } from "../../i18n";
import { IconFolder } from "../../icons";
import { PictureCards, type PictureCardItem } from "./PictureCards";

/** Last two segments of a repository path, normalized across POSIX and Windows. */
export function pathTail(path: string | undefined): string {
  if (!path) return "";
  const parts = path.split(/[/\\]+/).filter(Boolean);
  if (parts.length <= 2) return parts.join("/");
  return parts.slice(-2).join("/");
}

function FolderThumb({ repoId }: { repoId: string }) {
  const art = useFolderArt(repoId);
  if (!art) {
    return (
      <span className="picker-card-folder-icon" aria-hidden="true">
        <IconFolder size={28} />
      </span>
    );
  }
  return (
    <span className="picker-card-art picker-card-room" aria-hidden="true" data-room={art.roomId}>
      <img src={art.url} alt="" loading="lazy" draggable={false} onError={art.onError} />
    </span>
  );
}

interface RepoCardsCommon {
  repos: readonly RunnerRepo[];
  label?: string;
  simple?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  headerAction?: ReactNode;
  empty?: ReactNode;
  className?: string;
}

export interface SingleRepoCardsProps extends RepoCardsCommon {
  mode?: "one";
  value: string;
  onChange: (repoId: string) => void;
}

export interface MultiRepoCardsProps extends RepoCardsCommon {
  mode: "many";
  values: readonly string[];
  onChange: (repoIds: string[]) => void;
}

export type RepoCardsProps = SingleRepoCardsProps | MultiRepoCardsProps;

/** Folder picture cards: uses the active pack's rooms by stable repo-id hash
 * (loaded lazily), or a neutral SVG folder icon without rooms. Always shows
 * both repo name and path tail. */
export function RepoCards(props: RepoCardsProps) {
  const { t } = useI18n();
  const {
    repos,
    label,
    simple = false,
    disabled,
    invalid,
    describedBy,
    headerAction,
    empty,
    className,
  } = props;

  const items: PictureCardItem[] = repos.map((repo) => ({
    id: repo.id,
    label: repo.name,
    detail: repo.path ? pathTail(repo.path) : undefined,
    detailTitle: repo.path,
    visual: <FolderThumb repoId={repo.id} />,
  }));

  const resolvedLabel =
    label ?? (props.mode === "many" ? t("picker.repos") : t(simple ? "picker.simple.repos" : "picker.repos"));
  const rootClass = className ? `picker-repos ${className}` : "picker-repos";

  if (props.mode === "many") {
    return (
      <PictureCards
        mode="many"
        label={resolvedLabel}
        items={items}
        values={props.values}
        onChange={props.onChange}
        disabled={disabled}
        invalid={invalid}
        describedBy={describedBy}
        headerAction={headerAction}
        empty={empty}
        className={rootClass}
      />
    );
  }

  return (
    <PictureCards
      mode="one"
      label={resolvedLabel}
      items={items}
      value={props.value}
      onChange={props.onChange}
      disabled={disabled}
      invalid={invalid}
      describedBy={describedBy}
      headerAction={headerAction}
      empty={empty}
      className={rootClass}
    />
  );
}
