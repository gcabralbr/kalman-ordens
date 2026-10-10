package br.com.gammaquant.kalmanordens;

import org.junit.jupiter.api.Test;
import quickfix.DataDictionary;
import quickfix.Message;

import java.nio.charset.StandardCharsets;
import java.util.List;

/** O dsa_FIX44.xml contra mensagens reais do PnT (RoE 2025 e fixtures de 2019/2020). Relatório, não asserção. */
class DicionarioTest {

    static String montar(String pipe) {
        StringBuilder corpo = new StringBuilder();
        for (String p : pipe.replace("\u0001", "|").replace(" ", "").trim().split("[|]")) {
            if (p.isEmpty() || p.startsWith("8=") || p.startsWith("9=") || p.startsWith("10=")) continue;
            corpo.append(p).append('\u0001');
        }
        String s = "8=FIX.4.4\u00019=" + corpo.length() + "\u0001" + corpo;
        int soma = 0;
        for (byte b : s.getBytes(StandardCharsets.ISO_8859_1)) soma += b & 0xff;
        return s + String.format("10=%03d\u0001", soma % 256);
    }

    @Test
    void relatorio() throws Exception {
        DataDictionary padrao = new DataDictionary("fix/dsa_FIX44.xml");
        DataDictionary spec = new DataDictionary("fix/dsa_FIX44.xml");
        spec.setCheckUserDefinedFields(false);
        spec.setAllowUnknownMessageFields(true);
        spec.setCheckFieldsOutOfOrder(false);
        for (String f : List.of("ms2028.txt", "er1014.txt", "fillvwap.txt", "partially-filled.message", "rejected.message")) {
            String raw = montar(new String(getClass().getResourceAsStream("/mensagens/" + f).readAllBytes(), StandardCharsets.ISO_8859_1));
            for (var e : List.of(new Object[] {"padrao", padrao}, new Object[] {"spec", spec})) {
                DataDictionary dd = (DataDictionary) e[1];
                String r;
                try {
                    Message m = new Message();
                    m.fromString(raw, dd, true);
                    dd.validate(m);
                    r = "OK" + (m.hasGroup(555) ? " (555 grupos=" + m.getGroupCount(555) + ", 600 no corpo=" + m.isSetField(600) + ")" : "");
                } catch (Exception ex) {
                    r = "REJEITA: " + ex;
                }
                System.out.printf("QFJ %-26s %-7s %s%n", f, e[0], r);
            }
        }
    }
}
