import { IconButton, IqujiButton, IqujiInput } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { type ChangeEvent, type DragEvent, useRef, useState } from "react";
import { useI18n } from "@/shared/i18n";
import "./file-selector.css";

export type FileSelectorProps = {
  file: File | null;
  onFileChange: (file: File | null) => void;
  accept?: string;
  disabled?: boolean;
  ariaLabel?: string;
  buttonLabel?: string;
  placeholder?: string;
  fullWidth?: boolean;
  className?: string;
};

export function formatFileSize(bytes: number): string {
  if (bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function FileSelector({
  file,
  onFileChange,
  accept,
  disabled = false,
  ariaLabel,
  buttonLabel,
  placeholder,
  fullWidth = false,
  className = "",
}: FileSelectorProps) {
  const { t } = useI18n();
  const inputRef = useRef<HTMLInputElement>(null);
  const [isDragOver, setIsDragOver] = useState(false);

  const triggerPicker = () => {
    if (!disabled) {
      inputRef.current?.click();
    }
  };

  const handleChange = (e: ChangeEvent<HTMLInputElement>) => {
    const selected = e.target.files?.[0] ?? null;
    onFileChange(selected);
  };

  const handleClear = () => {
    if (inputRef.current) {
      inputRef.current.value = "";
    }
    onFileChange(null);
  };

  const handleDragOver = (e: DragEvent<HTMLDivElement>) => {
    if (disabled) return;
    e.preventDefault();
    e.stopPropagation();
    setIsDragOver(true);
  };

  const handleDragLeave = (e: DragEvent<HTMLDivElement>) => {
    if (disabled) return;
    e.preventDefault();
    e.stopPropagation();
    setIsDragOver(false);
  };

  const handleDrop = (e: DragEvent<HTMLDivElement>) => {
    if (disabled) return;
    e.preventDefault();
    e.stopPropagation();
    setIsDragOver(false);
    const droppedFile = e.dataTransfer.files?.[0];
    if (droppedFile) {
      onFileChange(droppedFile);
    }
  };

  return (
    <section
      aria-label={ariaLabel || buttonLabel || t("backup.selectFileAria")}
      className={`ops-file-selector ${fullWidth ? "ops-file-selector--block" : ""} ${isDragOver ? "ops-file-selector--drag-over" : ""} ${file ? "ops-file-selector--has-file" : ""} ${className}`}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <IqujiInput
        ref={inputRef}
        type="file"
        accept={accept}
        disabled={disabled}
        aria-label={ariaLabel || buttonLabel || t("backup.selectFileAria")}
        className="ops-file-selector__native"
        onChange={handleChange}
        tabIndex={-1}
      />

      {file ? (
        <div className="ops-file-selector__card">
          <IqujiButton
            compact
            variant="ghost"
            type="button"
            className="ops-file-selector__meta"
            onClick={triggerPicker}
            disabled={disabled}
            iconStart={<IqujiIcon decorative name="file-search" size={16} />}
          >
            <span className="ops-file-selector__info">
              <span className="ops-file-selector__name" title={file.name}>
                {file.name}
              </span>
              <span className="ops-file-selector__size">{formatFileSize(file.size)}</span>
            </span>
          </IqujiButton>
          {!disabled && (
            <IconButton
              compact
              label={t("common.remove")}
              icon={<IqujiIcon decorative name="close" size={13} />}
              onClick={handleClear}
            />
          )}
        </div>
      ) : (
        <div className="ops-file-selector__empty">
          <IqujiButton
            compact
            variant="secondary"
            type="button"
            disabled={disabled}
            iconStart={<IqujiIcon decorative name="upload" size={14} />}
            onClick={triggerPicker}
          >
            {buttonLabel || t("backup.selectFileAria")}
          </IqujiButton>
          <span className="ops-file-selector__hint">
            {placeholder || t("backup.selectFilePlaceholder")}
          </span>
        </div>
      )}
    </section>
  );
}
