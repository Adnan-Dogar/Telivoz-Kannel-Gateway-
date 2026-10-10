// File pickers for imports accept CSV/TXT (shown as editable text) and Excel (sent to the server as-is).
export const SHEET_TYPES =
  ".csv,.txt,.xlsx,text/csv,text/plain,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";

export const isExcel = (f: File) => /\.xlsx$/i.test(f.name);

export async function pickSheet(f: File | undefined, setText: (t: string) => void, setFile: (f: File | null) => void) {
  if (!f) return;
  if (isExcel(f)) {
    setFile(f);
    setText("");
  } else {
    setFile(null);
    setText(await f.text());
  }
}
