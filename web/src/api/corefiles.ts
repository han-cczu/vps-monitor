import type { CoreArch, CoreVersion } from 'src/types/corefiles';

import useSWR from 'swr';

import axios, { fetcher } from 'src/lib/axios';

const root = '/api/corefiles';
export function useCoreFiles() {
  const { data, error, isLoading, mutate } = useSWR<{
    versions: CoreVersion[];
    pinned_version: string;
  }>(root, fetcher);
  return {
    versions: data?.versions ?? [],
    pinnedVersion: data?.pinned_version ?? '',
    error,
    isLoading,
    refresh: mutate,
  };
}
export async function uploadCoreFile(
  version: string,
  arch: CoreArch,
  file: File,
  sha256: string,
  onProgress: (value: number) => void
) {
  const form = new FormData();
  form.append('version', version);
  form.append('arch', arch);
  form.append('sha256', sha256);
  form.append('file', file);
  await axios.post(root, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: 125_000,
    onUploadProgress: ({ loaded, total }) => {
      if (total) onProgress(Math.round((loaded * 100) / total));
    },
  });
}
export async function fetchCoreFile(version: string, arch: CoreArch, url: string, sha256: string) {
  await axios.post(`${root}/fetch`, { version, arch, url, sha256 }, { timeout: 125_000 });
}
export async function setCurrentCore(version: string) {
  await axios.put(`${root}/current`, { version });
}
export async function deleteCoreVersion(version: string) {
  await axios.delete(`${root}/${encodeURIComponent(version)}`);
}
