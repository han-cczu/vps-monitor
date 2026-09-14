export type CoreArch = 'amd64' | 'arm64';
export type CoreArtifact = { arch: CoreArch; sha256: string; size: number; uploaded_at: number };
export type CoreVersion = {
  version: string;
  arches: CoreArch[];
  files: CoreArtifact[];
  uploaded_at: number;
  current: boolean;
};
