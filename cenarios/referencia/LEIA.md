# Referência: log real de HML do PT

`fixpnt-hml-mascarado.log` — log do processo antigo do PT (QuickFIX/J, Spring Boot) contra a HML
do PnT DSA, sessão `GAMMAQ2 → PNT_DSA` em `54.94.189.36:45985`, out/nov 2025. 370 mensagens FIX
(258 recebidas, 112 enviadas). O usuário (553) e a senha (554) foram mascarados como `***` antes
de entrar no repositório (na HML eram iguais: mascarar só o 554 não protegia nada); por isso o 9
e o 10 dos dois logons enviados não batem — no fio batiam. Gravado com
`-text` (`.gitattributes`): os bytes, inclusive o SOH, não podem mudar.

O que ele mostra (SPEC v1.1 F14, F16):

- 9 e 10 corretos em todas as mensagens do PnT;
- estado de erro transitório com `20856=Y`: `5/5 → 7/7 → D/0 → F/1 → F/2`, reativação em 0–16 ms
  (todos os casos são multileg, 35=AB, depois de um replace; não há 35=D no log);
- `37` no formato `dsaGQ2.N`; `198` estável por `37`; `39=A` sem `198`;
- cancel sem 37 → `35=9 [DSA-40] … Robot Order not found`.

Usado por `pnt/logreal_test.go` (9/10 byte a byte; as 194 mensagens de aplicação do PnT passam
pela sessão do serviço sem 35=3 e sem descarte) e como modelo dos roteiros 30–33.

# Referência: saída com `[IS-72]` na HML (14/10/2026)

`saida-is72-hml-2026-10-14.log` — trecho do log FIX do serviço na .53 (binário `6866df3`, sessão
`GAMMAQ2 → PNT_DSA`): a saída da operação 900000002. Nove mensagens, uma por linha, com o carimbo
do log antes: o 35=D da saída, os dois 35=G, os ERs do PnT (`39=A`, `39=0`, `39=E`, `39=5`), o
35=9 `[IS-72]`/`[DSA-56]` e o fill no ClOrdID vigente. SOH de volta no lugar do `|` da cópia;
9 e 10 conferem byte a byte (inclusive os espaços no fim do 58 do 35=9). Sem logon: nenhuma senha.
Gravado com `-text`.

Usado por `pnt/logreal_test.go` (`TestLogSaidaIS72`) e pelo roteiro 51 (mesmos campos, na ordem
do fio).

# Referência: disparo da 1014 na HML (14/10/2026)

`disparo-1014-hml-2026-10-14.log` — trecho do log FIX do serviço na .53, as dez mensagens do PnT
da 1014 da operação 900000003 (`37=dsaGQ2.28621`): `39=A`, `39=0`, quatro `150=I 39=B` com
`20807=1`/`20806=211525` e `[DSA-122]` (o disparo), o fill (`150=F`, 211430), dois `150=I 39=B`
"Delaying final state 'Finished'" e o `150=I 39=2` final. Mesmo formato da anterior; 9 e 10
conferem byte a byte. Usado por `pnt/logreal_test.go` (`TestLogDisparo1014`) e pelo roteiro 05.
